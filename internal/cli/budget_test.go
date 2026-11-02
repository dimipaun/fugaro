package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/pricing/catalog"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/watch"
)

// budgetFixture is a project config whose budget backend is the RTDB fake.
type budgetFixture struct {
	db  *gcpfake.RTDB
	dir string // the local config's directory; its runs bucket is dir/runs
}

const (
	usd1   = int64(1_000_000)
	nodeCG = "config/caps/global"
)

func newBudgetFixture(t *testing.T, budgetYAML string) *budgetFixture {
	t.Helper()
	dir := t.TempDir()
	f := &budgetFixture{db: gcpfake.NewRTDB(t), dir: dir}
	path := isolateProjects(t, dir)
	if budgetYAML == "" {
		// The ordinary installation's budget block: the database and the
		// Firebase project it belongs to, which here is the installation's own
		// GCP project (budget.firebase_project == gcp_project).
		budgetYAML = "budget: { mode: observe, per_run_usd: 5, firebase_project: proj-1234, rtdb_url: " + f.db.URL + " }\n"
	}
	cfg := "version: 1\nname: aurora\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: unused-bucket\n" +
		"bucket_url: file://" + filepath.Join(dir, "runs") + "\nuser: someone@example.com\nmax_parallel: 2\n" +
		budgetYAML + "endpoints: { no_auth: true }\n" +
		"repos:\n  acme/app: { provider: github, base_branch: main, workflows: [web] }\n"
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUGARO_CONFIG", path)
	f.db.Set("fugaro/project", "aurora")
	f.db.Set(budget.PathMark, auroraMark())
	// The installation names its project in the runs bucket, as its
	// Terraform does (mirrors cloudFixture.writeMarker): watch's queued-run
	// lookup checks it before trusting the bucket.
	f.writeMarker(t, "aurora", "proj-1234")
	return f
}

func (f *budgetFixture) runsDir() string { return filepath.Join(f.dir, "runs") }

// appendConfig adds top-level YAML (such as claude_accounts: …) to the local
// config, the way cloudFixture.appendConfig does.
func (f *budgetFixture) appendConfig(t *testing.T, yaml string) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, []byte(yaml)...), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *budgetFixture) writeMarker(t *testing.T, name, gcpProject string) {
	t.Helper()
	data, err := json.Marshal(infra.ProjectMarker{Version: 1, Name: name, GCPProject: gcpProject})
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(f.runsDir(), filepath.FromSlash(infra.ProjectMarkerObject))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// typed makes stdin a terminal, so a confirmation can be typed.
func typed(t *testing.T) {
	t.Helper()
	old := stdinIsTerminal
	stdinIsTerminal = func(io.Reader) bool { return true }
	t.Cleanup(func() { stdinIsTerminal = old })
}

func today() int64 { return budget.Day(time.Now()) }

func seedCaps(f *budgetFixture, daily, perRun int64) {
	f.db.Set(nodeCG, map[string]any{"dailyMicros": daily * usd1, "perRunMicros": perRun * usd1})
}

func TestShowSanitizesRegistryText(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.db.Set(budget.PathAgent(appSlug, "20261002-090000-aaaa"), map[string]any{
		"repo": "acme/app", "title": "fix \x1b]52;c;AAAA\x07the bug\x1b[2J", "stage": "code\x1b[31m red",
		"workflow": "web", "updatedAt": time.Now().UnixMilli(), "startedAt": time.Now().UnixMilli(),
	})
	out, errOut, err := execute(t, "budget", "show", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "\x1b") || strings.Contains(errOut, "\x1b") || strings.Contains(out, "\x07") {
		t.Fatalf("terminal controls reached the output: %q", out)
	}
	if !strings.Contains(out, "20261002-090000-aaaa") || !strings.Contains(out, "the bug") {
		t.Fatalf("the running entry is missing:\n%s", out)
	}
	js, _, err := execute(t, "budget", "show", "--all", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(js, 0x1b) || !json.Valid([]byte(js)) {
		t.Fatalf("json output has a raw escape or is invalid: %q", js)
	}
}

func TestShowJSONProject(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.db.Set("config/mode", "enforce")
	seedCaps(f, 100, 20)
	f.db.Set(budget.PathCapsDefaults, map[string]any{"repoDailyMicros": 40 * usd1, "repoPerRunMicros": 10 * usd1})
	f.db.Set("config/limits", map[string]any{"maxReserveMicros": 2 * usd1})
	d := today()
	f.db.Set(budget.PathSpendGlobal(d), map[string]any{"counted": 30 * usd1, "spent": 12 * usd1, "notional": 3 * usd1})
	f.db.Set(budget.PathSpendRepo(d, appSlug), map[string]any{"counted": 25 * usd1, "spent": 10 * usd1})
	f.db.Set(budget.PathKillRepo(appSlug), map[string]any{"on": true, "by": "boss@example.com", "at": int64(1_700_000_000_000), "reason": "runaway"})

	out, errOut, err := execute(t, "budget", "show", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(errOut, "project: aurora (GCP proj-1234)\n") {
		t.Fatalf("stderr = %q, want the project header first", errOut)
	}
	var doc struct {
		Project       string   `json:"project"`
		Mode          string   `json:"mode"`
		MaxReserveUSD *float64 `json:"max_reserve_usd"`
		Global        struct {
			DailyCapUSD *float64 `json:"daily_cap_usd"`
			CountedUSD  float64  `json:"counted_usd"`
			NotionalUSD float64  `json:"notional_usd"`
			HeadroomUSD *float64 `json:"headroom_usd"`
		} `json:"global"`
		Repos []struct {
			Slug            string   `json:"slug"`
			Repo            string   `json:"repo"`
			CountedUSD      float64  `json:"counted_usd"`
			DailyCapUSD     *float64 `json:"daily_cap_usd"`
			CapSource       string   `json:"daily_cap_source"`
			PerRunCapUSD    *float64 `json:"per_run_cap_usd"`
			PerRunCapSource string   `json:"per_run_cap_source"`
			HeadroomUSD     *float64 `json:"headroom_usd"`
			Kill            *struct {
				By     string `json:"by"`
				Reason string `json:"reason"`
			} `json:"kill"`
		} `json:"repos"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if doc.Project != "aurora" || doc.Mode != "enforce" || doc.MaxReserveUSD == nil || *doc.MaxReserveUSD != 2 {
		t.Errorf("doc = %+v", doc)
	}
	if doc.Global.DailyCapUSD == nil || *doc.Global.DailyCapUSD != 100 || doc.Global.CountedUSD != 30 || doc.Global.NotionalUSD != 3 ||
		doc.Global.HeadroomUSD == nil || *doc.Global.HeadroomUSD != 70 {
		t.Errorf("global = %+v", doc.Global)
	}
	if len(doc.Repos) != 1 {
		t.Fatalf("repos = %+v", doc.Repos)
	}
	r := doc.Repos[0]
	// The repository has no cap node of its own: the default applies, and the
	// headroom is the smaller of 40-25 and 100-30. The per-run cap is the
	// effective one (min(default 10, global 20)), from the default.
	if r.Slug != appSlug || r.Repo != "acme/app" || r.CountedUSD != 25 || r.DailyCapUSD == nil || *r.DailyCapUSD != 40 ||
		r.CapSource != "default" || r.HeadroomUSD == nil || *r.HeadroomUSD != 15 {
		t.Errorf("repo = %+v", r)
	}
	if r.PerRunCapUSD == nil || *r.PerRunCapUSD != 10 || r.PerRunCapSource != "default" {
		t.Errorf("per-run cap = %v (%s), want $10 from the default", r.PerRunCapUSD, r.PerRunCapSource)
	}
	if r.Kill == nil || r.Kill.By != "boss@example.com" || r.Kill.Reason != "runaway" {
		t.Errorf("kill = %+v", r.Kill)
	}
}

// TestShowPerRunCapIsTheEffectiveOne: show prints the cap a run is really
// held to and the scope it comes from -- the repository's own node, the
// default, or the project's global cap, which is the fallback of a repository
// that has neither (D6).
func TestShowPerRunCapIsTheEffectiveOne(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.db.Set("config/mode", "enforce")
	seedCaps(f, 100, 20) // the global per-run cap: $20
	f.db.Set(budget.PathSpendRepo(today(), appSlug), map[string]any{"counted": 5 * usd1})
	type perRun struct {
		Cap    *float64
		Source string
	}
	repos := func(t *testing.T) perRun {
		t.Helper()
		out, _, err := execute(t, "budget", "show", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Repos []struct {
				Slug   string   `json:"slug"`
				Cap    *float64 `json:"per_run_cap_usd"`
				Source string   `json:"per_run_cap_source"`
			} `json:"repos"`
		}
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		for _, r := range doc.Repos {
			if r.Slug == appSlug {
				return perRun{r.Cap, r.Source}
			}
		}
		t.Fatalf("no row for %s in:\n%s", appSlug, out)
		return perRun{}
	}
	// No cap of its own and no default: the global cap binds it.
	if r := repos(t); r.Cap == nil || *r.Cap != 20 || r.Source != "global" {
		t.Fatalf("per-run cap = %v (%s), want $20 from the global cap", r.Cap, r.Source)
	}
	// A default under the global cap is the one that binds.
	f.db.Set(budget.PathCapsDefaults, map[string]any{"repoPerRunMicros": 8 * usd1})
	if r := repos(t); r.Cap == nil || *r.Cap != 8 || r.Source != "default" {
		t.Fatalf("per-run cap = %v (%s), want $8 from the default", r.Cap, r.Source)
	}
	// The repository's own node wins over the default, and the global cap
	// still bounds it.
	f.db.Set(budget.PathCapsRepo(appSlug), map[string]any{"repo": "acme/app", "perRunMicros": 30 * usd1})
	if r := repos(t); r.Cap == nil || *r.Cap != 20 || r.Source != "global" {
		t.Fatalf("per-run cap = %v (%s), want the global $20 under the repository's own $30", r.Cap, r.Source)
	}
	f.db.Set(budget.PathCapsRepo(appSlug), map[string]any{"repo": "acme/app", "perRunMicros": 12 * usd1})
	if r := repos(t); r.Cap == nil || *r.Cap != 12 || r.Source != "repo" {
		t.Fatalf("per-run cap = %v (%s), want the repository's own $12", r.Cap, r.Source)
	}
	// Nothing anywhere: no per-run cap, which a run in enforce halts on.
	f.db.Set(budget.PathCapsRepo(appSlug), map[string]any{"repo": "acme/app"})
	f.db.Set(budget.PathCapsDefaults, nil)
	f.db.Set(nodeCG, map[string]any{"dailyMicros": 100 * usd1})
	out, _, err := execute(t, "budget", "show")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "per-run cap not set (no per-run cap anywhere)") {
		t.Fatalf("show must say no per-run cap is set:\n%s", out)
	}
}

func TestShowSaysModeIsUnset(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedCaps(f, 100, 20)
	out, _, err := execute(t, "budget", "show")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "not set") || !strings.Contains(out, "observe") {
		t.Fatalf("show must say the mode is unset and acts as observe:\n%s", out)
	}
}

func TestShowRefusesAnotherProjectsDatabase(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.db.Set("fugaro/project", "birch")
	_, _, err := execute(t, "budget", "show")
	if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "birch") {
		t.Fatalf("err = %v", err)
	}
}

func TestShowUnreachableIsExit2(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.db.Refuse(503, "UNAVAILABLE", "", "down")
	_, _, err := execute(t, "budget", "show")
	if err == nil || ExitCode(err) != ExitRemoteError {
		t.Fatalf("err = %v, code %d", err, ExitCode(err))
	}
}

func TestSetShowsOldAndNew(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedCaps(f, 100, 20)
	out, _, err := execute(t, "budget", "set", "--global", "--daily", "60")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{nodeCG, "$100", "$60"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	got := f.db.Value(nodeCG).(map[string]any)
	if got["dailyMicros"].(json.Number).String() != "60000000" || got["perRunMicros"].(json.Number).String() != "20000000" {
		t.Fatalf("node = %v", got)
	}
}

func TestSetRepoAndDefaultsUseTheirOwnKeys(t *testing.T) {
	f := newBudgetFixture(t, "")
	if _, _, err := execute(t, "budget", "set", "--defaults", "--daily", "40", "--per-run", "10", "--yes"); err != nil {
		t.Fatal(err)
	}
	d := f.db.Value(budget.PathCapsDefaults).(map[string]any)
	if d["repoDailyMicros"].(json.Number).String() != "40000000" || d["repoPerRunMicros"].(json.Number).String() != "10000000" {
		t.Fatalf("defaults = %v", d)
	}
	if _, _, err := execute(t, "budget", "set", "--repo", "acme/app", "--daily", "5", "--yes"); err != nil {
		t.Fatal(err)
	}
	r := f.db.Value(budget.PathCapsRepo(appSlug)).(map[string]any)
	if r["dailyMicros"].(json.Number).String() != "5000000" || r["repo"] != "acme/app" {
		t.Fatalf("repo caps = %v", r)
	}
}

func TestSetRaiseNeedsConfirmation(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedCaps(f, 100, 20)
	// No terminal, no --yes: refused, nothing written.
	_, _, err := execute(t, "budget", "set", "--global", "--daily", "150")
	if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "needs a real terminal: "+initflow.NoTerminalAdvice) {
		t.Fatalf("err = %v", err)
	}
	if v := f.db.Value(nodeCG).(map[string]any)["dailyMicros"].(json.Number).String(); v != "100000000" {
		t.Fatalf("the cap moved without a confirmation: %s", v)
	}
	// A wrong name typed at a terminal: refused.
	typed(t)
	if _, _, err := executeStdin(t, "birch\n", "budget", "set", "--global", "--daily", "150"); err == nil {
		t.Fatal("a wrong project name confirmed the raise")
	}
	// The project's name: applied.
	if _, _, err := executeStdin(t, "aurora\n", "budget", "set", "--global", "--daily", "150"); err != nil {
		t.Fatal(err)
	}
	if v := f.db.Value(nodeCG).(map[string]any)["dailyMicros"].(json.Number).String(); v != "150000000" {
		t.Fatalf("daily = %s", v)
	}
	// --yes needs no terminal.
	stdinIsTerminal = func(io.Reader) bool { return false }
	if _, _, err := execute(t, "budget", "set", "--global", "--daily", "200", "--yes"); err != nil {
		t.Fatal(err)
	}
}

func TestSetFromNothingCountsAsRaise(t *testing.T) {
	f := newBudgetFixture(t, "")
	_, _, err := execute(t, "budget", "set", "--global", "--daily", "50")
	if err == nil || !strings.Contains(err.Error(), "confirmation") {
		t.Fatalf("err = %v", err)
	}
	if f.db.Value(nodeCG) != nil {
		t.Fatal("a cap was written without a confirmation")
	}
}

func TestSetLowerNoConfirmation(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedCaps(f, 100, 20)
	f.db.Set("config/mode", "enforce")
	f.db.Set("config/limits", map[string]any{"maxReserveMicros": 2 * usd1})
	for _, args := range [][]string{
		{"--global", "--daily", "50"},
		{"--global", "--per-run", "10"},
		{"--global", "--max-reserve", "1"},
		{"--global", "--mode", "enforce"}, // unchanged
		{"--global", "--clear"},
	} {
		if _, _, err := execute(t, append([]string{"budget", "set"}, args...)...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if f.db.Value(nodeCG) != nil {
		t.Fatalf("--clear left %v", f.db.Value(nodeCG))
	}
	// enforce to observe loosens every cap check: that one confirms.
	if _, _, err := execute(t, "budget", "set", "--global", "--mode", "observe"); err == nil {
		t.Fatal("loosening the mode needed no confirmation")
	}
}

// TestSetMaxReserveKeepsOtherLimitKeys: config/limits is a read-modify-written
// node that holds more than the largest lease — init writes the
// rules-generation marker (projectpolicy.PathRulesMarker) beside it after a
// rules deploy. budget set edits the keys it owns and must never replace the
// node: a wipe would leave a database whose rules carry the policy nodes with a
// missing marker, which halts every budget-on run once the runner reads it
// (phase 1 task 1.3).
func TestSetMaxReserveKeepsOtherLimitKeys(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedCaps(f, 100, 20)
	markerKey := strings.TrimPrefix(projectpolicy.PathRulesMarker(), budget.PathLimits+"/")
	if markerKey == projectpolicy.PathRulesMarker() {
		t.Fatalf("PathRulesMarker %q does not sit under %q", projectpolicy.PathRulesMarker(), budget.PathLimits)
	}
	f.db.Set(budget.PathLimits, map[string]any{"maxReserveMicros": 2 * usd1, markerKey: projectpolicy.RulesVersion})
	if _, _, err := execute(t, "budget", "set", "--global", "--max-reserve", "1"); err != nil {
		t.Fatal(err)
	}
	lim, ok := f.db.Value(budget.PathLimits).(map[string]any)
	if !ok {
		t.Fatalf("config/limits = %#v, want an object", f.db.Value(budget.PathLimits))
	}
	got, ok := lim[markerKey].(json.Number)
	if !ok || got.String() != strconv.Itoa(projectpolicy.RulesVersion) {
		t.Errorf("config/limits = %v: budget set dropped or changed the rules marker (%s), which the runner reads", lim, projectpolicy.PathRulesMarker())
	}
	if lim["maxReserveMicros"] == nil {
		t.Errorf("config/limits = %v: the largest lease was not written", lim)
	}
}

func TestSetValidatesRanges(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedCaps(f, 100, 20)
	for name, args := range map[string][]string{
		"negative":         {"--global", "--daily", "-1"},
		"too big":          {"--global", "--daily", "100001"},
		"nan":              {"--global", "--daily", "NaN"},
		"inf":              {"--global", "--per-run", "Inf"},
		"per-run > daily":  {"--global", "--daily", "10", "--per-run", "20"},
		"no scope":         {"--daily", "10"},
		"two scopes":       {"--global", "--defaults", "--daily", "10"},
		"nothing to set":   {"--global"},
		"bad mode":         {"--global", "--mode", "off"},
		"mode on a repo":   {"--repo", "acme/app", "--mode", "enforce"},
		"reserve on repo":  {"--repo", "acme/app", "--max-reserve", "1"},
		"clear and value":  {"--global", "--clear", "--daily", "5"},
		"repo and --all":   {"--global", "--repo", "acme/app", "--daily", "5"},
		"max reserve neg.": {"--global", "--max-reserve", "-0.5"},
	} {
		_, _, err := execute(t, append([]string{"budget", "set", "--yes"}, args...)...)
		if err == nil || ExitCode(err) != ExitUserError {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if n := len(f.db.Credentials()); n != 0 {
		t.Errorf("%d requests went out for refused arguments", n)
	}
	// The stored per-run cap bounds a new daily cap too.
	if _, _, err := execute(t, "budget", "set", "--global", "--daily", "10", "--yes"); err == nil || !strings.Contains(err.Error(), "per-run") {
		t.Errorf("a daily cap under the stored per-run cap was accepted: %v", err)
	}
}

// conflictOnce changes path in the fake just before the first PUT reaches it,
// as a second admin would.
type conflictOnce struct {
	rt   http.RoundTripper
	f    *budgetFixture
	path string
	val  any
	done bool
}

func (c *conflictOnce) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method == http.MethodPut && !c.done {
		c.done = true
		c.f.db.Set(c.path, c.val)
	}
	return c.rt.RoundTrip(r)
}

func TestSetETagConflictRetries(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedCaps(f, 100, 20)
	c := &conflictOnce{rt: http.DefaultTransport, f: f, path: nodeCG,
		val: map[string]any{"dailyMicros": 90 * usd1, "perRunMicros": 20 * usd1}}
	budgetTransport = c
	t.Cleanup(func() { budgetTransport = nil })
	out, _, err := execute(t, "budget", "set", "--global", "--daily", "50")
	if err != nil {
		t.Fatal(err)
	}
	if !c.done {
		t.Fatal("the conflict never happened")
	}
	got := f.db.Value(nodeCG).(map[string]any)
	if got["dailyMicros"].(json.Number).String() != "50000000" {
		t.Fatalf("node = %v", got)
	}
	// The retry shows the value it actually replaced.
	if !strings.Contains(out, "$90") || !strings.Contains(out, "changed") {
		t.Fatalf("the retry's output does not show the new old value:\n%s", out)
	}
}

func TestSetWithoutAdminExplains(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedCaps(f, 100, 20)
	f.db.DenyNext(1)
	_, _, err := execute(t, "budget", "set", "--global", "--daily", "50")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{"not a budget admin for project aurora", "owners", "budget_admins"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
}

func TestKillAllTypesProjectName(t *testing.T) {
	f := newBudgetFixture(t, "")
	_, _, err := execute(t, "budget", "kill", "--all")
	if err == nil || f.db.Value(budget.PathKillGlobal) != nil {
		t.Fatalf("kill --all without a confirmation: %v", err)
	}
	typed(t)
	if _, _, err := executeStdin(t, "yes\n", "budget", "kill", "--all"); err == nil || f.db.Value(budget.PathKillGlobal) != nil {
		t.Fatalf("kill --all accepted something other than the project's name: %v", err)
	}
	if _, _, err := executeStdin(t, "aurora\n", "budget", "kill", "--all", "--reason", "runaway spend"); err != nil {
		t.Fatal(err)
	}
	if k, _ := f.db.Value(budget.PathKillGlobal).(map[string]any); k["on"] != true {
		t.Fatalf("switch = %v", k)
	}
}

func TestKillRecordsBy(t *testing.T) {
	f := newBudgetFixture(t, "")
	before := time.Now().UnixMilli()
	out, _, err := execute(t, "budget", "kill", "--repo", "acme/app", "--reason", "looping")
	if err != nil {
		t.Fatal(err)
	}
	k, _ := f.db.Value(budget.PathKillRepo(appSlug)).(map[string]any)
	at, _ := k["at"].(json.Number).Int64()
	if k["on"] != true || k["by"] != "someone@example.com" || k["reason"] != "looping" || at < before || at > time.Now().UnixMilli() {
		t.Fatalf("switch = %v", k)
	}
	if !strings.Contains(out, "acme/app") {
		t.Fatalf("output = %q", out)
	}
	// Killing again changes nothing and says so.
	out, _, err = execute(t, "budget", "kill", "--repo", "acme/app", "--reason", "again")
	if err != nil || !strings.Contains(out, "already") {
		t.Fatalf("err = %v, out = %q", err, out)
	}
	if k2 := f.db.Value(budget.PathKillRepo(appSlug)).(map[string]any); k2["reason"] != "looping" {
		t.Fatalf("a second kill rewrote the switch: %v", k2)
	}
	if _, _, err := execute(t, "budget", "kill", "--repo", "acme/app", "--all"); err == nil {
		t.Fatal("--repo and --all together were accepted")
	}
	if _, _, err := execute(t, "budget", "kill", "--repo", "acme/app", "--reason", strings.Repeat("x", 201)); err == nil {
		t.Fatal("an over-long reason was accepted")
	}
}

func TestResumeConfirms(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.db.Set(budget.PathKillGlobal, map[string]any{"on": true, "by": "boss@example.com", "at": int64(1)})
	if _, _, err := execute(t, "budget", "resume", "--all"); err == nil {
		t.Fatal("resume needed no confirmation")
	}
	if k := f.db.Value(budget.PathKillGlobal).(map[string]any); k["on"] != true {
		t.Fatalf("the switch moved: %v", k)
	}
	if _, _, err := execute(t, "budget", "resume", "--all", "--yes"); err != nil {
		t.Fatal(err)
	}
	k := f.db.Value(budget.PathKillGlobal).(map[string]any)
	if k["on"] != false || k["by"] != "someone@example.com" {
		t.Fatalf("switch after resume = %v", k)
	}
	if out, _, err := execute(t, "budget", "resume", "--all", "--yes"); err != nil || !strings.Contains(out, "not killed") {
		t.Fatalf("err = %v, out = %q", err, out)
	}
}

func TestPricesOfflineAndStaleWarning(t *testing.T) {
	// No project config at all, and no network: prices still works.
	isolateProjects(t, t.TempDir())
	old := budgetNow
	t.Cleanup(func() { budgetNow = old })

	budgetNow = func() time.Time { return time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC) }
	out, errOut, err := execute(t, "budget", "prices")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "claude-sonnet-5-5") || !strings.Contains(out, "2026-10-20") || !strings.Contains(out, "pricing") {
		t.Fatalf("table:\n%s", out)
	}
	if strings.Contains(errOut, "older") {
		t.Fatalf("a fresh table was called stale: %q", errOut)
	}

	budgetNow = func() time.Time { return time.Date(2027, 1, 25, 0, 0, 0, 0, time.UTC) }
	_, errOut, err = execute(t, "budget", "prices")
	if err != nil || !strings.Contains(errOut, "older than 90 days") {
		t.Fatalf("err = %v, stderr = %q", err, errOut)
	}
	js, _, err := execute(t, "budget", "prices", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		CheckedAt string `json:"checked_at"`
		Stale     bool   `json:"stale"`
		Models    []struct {
			ID         string  `json:"id"`
			InputPerM  float64 `json:"input_per_m"`
			OutputPerM float64 `json:"output_per_m"`
		} `json:"models"`
	}
	if err := json.Unmarshal([]byte(js), &doc); err != nil || doc.CheckedAt != "2026-10-20" || !doc.Stale || len(doc.Models) < 3 {
		t.Fatalf("err = %v, doc = %+v", err, doc)
	}
}

func TestPricesShowsLocalOverrides(t *testing.T) {
	newBudgetFixture(t, "budget: { mode: observe, per_run_usd: 5, rtdb_url: http://127.0.0.1:9 }\n"+
		"model_prices:\n  claude-sonnet-5-5: { input_per_m: 3, output_per_m: 15 }\n")
	out, _, err := execute(t, "budget", "prices")
	if err != nil {
		t.Fatal(err)
	}
	line := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "claude-sonnet-5-5") {
			line = l
		}
	}
	if !strings.Contains(line, "$3") || !strings.Contains(line, "local") {
		t.Fatalf("the override is not shown:\n%s", out)
	}
}

// TestBudgetPricesJSONPinsTheFigures is the price pin M1 needs: the document
// `budget prices --json` prints is held against rows written out here, field
// for field — one embedded Claude row, one row the built-in provider price
// catalog priced, and one the owner's own model_prices replaced. The figures
// are the pin; comparing the command against the same builder the test calls
// would agree with any figure that builder had come to produce.
//
// Mutation (run, restored): double CacheReadPerM in priceRowOf and this fails
// on claude-opus-5-5's cache_read_per_m 0.4 against 0.2.
func TestBudgetPricesJSONPinsTheFigures(t *testing.T) {
	modelsFixture(t)

	out, _, err := execute(t, "budget", "prices", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var doc pricesDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	byID := map[string]priceRow{}
	for _, r := range doc.Models {
		byID[r.ID] = r
	}
	for _, want := range []struct{ id, row string }{
		// The embedded table's own figures: $4/$20 a million, cache reads at
		// 0.05x and cache writes at 1.25x and 2x, a search at $10 a thousand,
		// and no source of its own (the document's is the table's).
		{"claude-opus-5-5", `{"id":"claude-opus-5-5","input_per_m":4,"output_per_m":20,"cache_read_per_m":0.2,"cache_write_5m_per_m":5,"cache_write_1h_per_m":8,"web_search_per_1k":10,"local":false,"unverified":false}`},
		// The catalog row the openrouter route claims: the zdr tier's cheapest
		// end, its 15 endpoints (10 of them ZDR-eligible) and the dearest price
		// the tier carries, which is the most a call it serves can be billed.
		{"deepseek/deepseek-v4-flash", `{"id":"deepseek/deepseek-v4-flash","input_per_m":0.05,"output_per_m":0.17,"cache_read_per_m":0.013999999999999999,"cache_write_5m_per_m":0.0625,"cache_write_1h_per_m":0.1,"web_search_per_1k":0,"local":false,"unverified":false,"catalog":{"tier":"zdr","bound":"min","endpoints":15,"zdr_endpoints":10,"source":"https://openrouter.ai/api/v1/models","checked_at":"2026-10-23","stale":false,"tier_input_max_per_m":0.7,"tier_output_max_per_m":1.32}}`},
		// The override: the owner's own numbers, marked local, with the cache
		// multipliers the table's defaults give them.
		{"vendor/model-y", `{"id":"vendor/model-y","input_per_m":1,"output_per_m":2,"cache_read_per_m":0.1,"cache_write_5m_per_m":1.25,"cache_write_1h_per_m":2,"web_search_per_1k":10,"local":true,"unverified":false}`},
	} {
		row, ok := byID[want.id]
		if !ok {
			t.Errorf("no row for %s:\n%s", want.id, out)
			continue
		}
		if got := jsonOf(t, row); got != want.row {
			t.Errorf("%s reads\n%s\nand the pin is\n%s", want.id, got, want.row)
		}
	}
	// And the whole document is the shared builder's rows, in its order: the
	// figures above are pinned, and no second row construction can grow beside
	// the builder both commands call (D794).
	table, ov, err := priceTable(selectedModelsConfig(t), budgetNow())
	if err != nil {
		t.Fatal(err)
	}
	want := priceRowsFor(table, ov)
	if len(want) != len(doc.Models) {
		t.Fatalf("the command prints %d rows and the shared builder has %d", len(doc.Models), len(want))
	}
	for i := range want {
		got, wantJSON := jsonOf(t, doc.Models[i]), jsonOf(t, want[i])
		if got != wantJSON {
			t.Errorf("row %d is %s, and the shared builder makes %s", i, got, wantJSON)
		}
	}
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestBudgetRefusesWithoutFirebaseConfig(t *testing.T) {
	newBudgetFixture(t, "budget: { mode: observe, per_run_usd: 5 }\n")
	for _, args := range [][]string{{"show"}, {"set", "--global", "--daily", "1", "--yes"}, {"kill", "--all", "--yes"}, {"resume", "--all", "--yes"}} {
		_, _, err := execute(t, append([]string{"budget"}, args...)...)
		if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "update --firebase-project") {
			t.Errorf("%v: err = %v", args, err)
		}
	}
	// No budget block at all.
	newBudgetFixture(t, " \n")
	if _, _, err := execute(t, "budget", "show"); err == nil || !strings.Contains(err.Error(), "update --firebase-project") {
		t.Errorf("err = %v", err)
	}
}

func TestSetClearRepoFallingBackToHigherDefaultConfirms(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.db.Set(budget.PathCapsRepo(appSlug), map[string]any{"dailyMicros": 5 * usd1, "repo": "acme/app"})
	f.db.Set(budget.PathCapsDefaults, map[string]any{"repoDailyMicros": 60 * usd1})
	if _, _, err := execute(t, "budget", "set", "--repo", "acme/app", "--clear"); err == nil || !strings.Contains(err.Error(), "confirmation") {
		t.Fatalf("err = %v", err)
	}
	if f.db.Value(budget.PathCapsRepo(appSlug)) == nil {
		t.Fatal("the cap was removed unconfirmed")
	}
	// No default at all is confirmed too; a default that is lower is not.
	f.db.Set(budget.PathCapsDefaults, nil)
	if _, _, err := execute(t, "budget", "set", "--repo", "acme/app", "--clear"); err == nil {
		t.Fatal("clearing to no cap needed no confirmation")
	}
	f.db.Set(budget.PathCapsDefaults, map[string]any{"repoDailyMicros": 2 * usd1})
	if _, _, err := execute(t, "budget", "set", "--repo", "acme/app", "--clear"); err != nil {
		t.Fatal(err)
	}
	if f.db.Value(budget.PathCapsRepo(appSlug)) != nil {
		t.Fatal("not cleared")
	}
}

func TestSetClearDefaultsPerRunFallingBackToGlobalConfirms(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.db.Set(budget.PathCapsGlobal, map[string]any{"perRunMicros": 20 * usd1})
	f.db.Set(budget.PathCapsDefaults, map[string]any{"repoPerRunMicros": 5 * usd1})
	// Clearing the defaults hands every repository without a node of its
	// own to the $20 global cap: $5 -> $20 is a raise to confirm.
	if _, _, err := execute(t, "budget", "set", "--defaults", "--clear"); err == nil || !strings.Contains(err.Error(), "confirmation") {
		t.Fatalf("err = %v", err)
	}
	if f.db.Value(budget.PathCapsDefaults) == nil {
		t.Fatal("the defaults were removed unconfirmed")
	}
	// A default above the global cap changes nothing effective ($20
	// before and after): no confirmation.
	f.db.Set(budget.PathCapsDefaults, map[string]any{"repoPerRunMicros": 50 * usd1})
	if _, _, err := execute(t, "budget", "set", "--defaults", "--clear"); err != nil {
		t.Fatal(err)
	}
	if f.db.Value(budget.PathCapsDefaults) != nil {
		t.Fatal("not cleared")
	}
	// A daily-only default node leaves the per-run side unset before and
	// after: clearing it tightens, and needs no confirmation.
	f.db.Set(budget.PathCapsDefaults, map[string]any{"repoDailyMicros": 60 * usd1})
	if _, _, err := execute(t, "budget", "set", "--defaults", "--clear"); err != nil {
		t.Fatal(err)
	}
	// With no global cap the default is the only per-run cap there is:
	// clearing it leaves every repository without one, which is confirmed
	// too (as clearing a repository's own cap is).
	f.db.Set(budget.PathCapsGlobal, nil)
	f.db.Set(budget.PathCapsDefaults, map[string]any{"repoPerRunMicros": 5 * usd1})
	if _, _, err := execute(t, "budget", "set", "--defaults", "--clear"); err == nil || !strings.Contains(err.Error(), "confirmation") {
		t.Fatalf("clearing to no cap needed no confirmation: err = %v", err)
	}
	if _, _, err := execute(t, "budget", "set", "--defaults", "--clear", "--yes"); err != nil {
		t.Fatal(err)
	}
	if f.db.Value(budget.PathCapsDefaults) != nil {
		t.Fatal("not cleared")
	}
}

func TestShowRefusesNonStringProjectMark(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.db.Set("fugaro/project", map[string]any{"name": "aurora"})
	_, _, err := execute(t, "budget", "show")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v", err)
	}
}

func TestBudgetRefusesBadRTDBURL(t *testing.T) {
	// A config that loads (no_auth lifts the loopback rule) is still
	// checked at open time when it has credentials.
	newBudgetFixture(t, "budget: { mode: observe, per_run_usd: 5, rtdb_url: \"https://evil.example.com\" }\n")
	if _, _, err := execute(t, "budget", "show"); err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v", err)
	}
}

func TestResumeRepoWarnsWhileGlobalKillOn(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.db.Set(budget.PathKillGlobal, map[string]any{"on": true, "by": "boss@example.com", "at": int64(1)})
	f.db.Set(budget.PathKillRepo(appSlug), map[string]any{"on": true, "by": "x", "at": int64(1)})
	out, _, err := execute(t, "budget", "resume", "--repo", "acme/app", "--yes")
	if err != nil || !strings.Contains(out, "project-wide kill switch is still on") {
		t.Fatalf("err = %v, out = %q", err, out)
	}
}

func TestSetPartialFailureNamesWhatWasWritten(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.db.Set("config/mode", "enforce")
	seedCaps(f, 100, 20)
	f.db.Deny("config/mode")
	// Tightening (the daily cap) goes first; the mode loosening then fails.
	_, _, err := execute(t, "budget", "set", "--global", "--daily", "50", "--mode", "observe", "--yes")
	if err == nil || !strings.Contains(err.Error(), "partly applied") || !strings.Contains(err.Error(), nodeCG) || !strings.Contains(err.Error(), "config/mode") {
		t.Fatalf("err = %v", err)
	}
}

func TestSetEnforceNeedsGlobalCaps(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.db.Set("config/mode", "observe")
	_, _, err := execute(t, "budget", "set", "--global", "--mode", "enforce", "--yes")
	if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "global caps") {
		t.Fatalf("err = %v, want a refusal naming the caps", err)
	}
	if f.db.Value("config/mode") != "observe" {
		t.Fatal("the mode was written")
	}
	// Only a daily cap in the database: still refused; giving the per-run
	// cap in the same command is enough.
	f.db.Set(nodeCG, map[string]any{"dailyMicros": 100 * usd1})
	if _, _, err := execute(t, "budget", "set", "--global", "--mode", "enforce", "--yes"); err == nil {
		t.Fatal("enforce was accepted with no per-run cap")
	}
	if _, _, err := execute(t, "budget", "set", "--global", "--per-run", "20", "--mode", "enforce", "--yes"); err != nil {
		t.Fatal(err)
	}
	if f.db.Value("config/mode") != "enforce" {
		t.Fatal("the mode was not written")
	}
}

// TestTUIAndCLIShareSetKill: the watch TUI and `budget kill|resume` write the
// same nodes with the same fields, because both go through budget.SetKill.
func TestTUIAndCLIShareSetKill(t *testing.T) {
	f := newBudgetFixture(t, "")
	db, err := rtdb.New(f.db.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	shape := func(path string) map[string]any {
		m, _ := f.db.Value(path).(map[string]any)
		out := map[string]any{}
		for k, v := range m {
			out[k] = v
		}
		if out["at"] == nil {
			t.Fatalf("%s has no timestamp: %v", path, m)
		}
		out["at"] = "<at>"
		return out
	}
	// CLI kill, then reset, then the TUI's executor on the same node.
	if _, _, err := execute(t, "budget", "kill", "--repo", "acme/app", "--reason", "looping"); err != nil {
		t.Fatal(err)
	}
	viaCLI := shape(budget.PathKillRepo(appSlug))
	f.db.Set(budget.PathKillRepo(appSlug), nil)
	o := watch.Execute(context.Background(), db, watch.Request{Kind: watch.KillRepo, Project: "aurora", Target: watch.Target{Slug: appSlug, Name: "acme/app"}, Reason: "looping"}, "someone@example.com")
	if !o.Written {
		t.Fatalf("%+v", o)
	}
	if viaTUI := shape(budget.PathKillRepo(appSlug)); !reflect.DeepEqual(viaCLI, viaTUI) {
		t.Fatalf("CLI wrote %v, TUI wrote %v", viaCLI, viaTUI)
	}
	// Resume, both ways, ends in the same node too.
	if _, _, err := execute(t, "budget", "resume", "--repo", "acme/app", "--yes", "--reason", "ok"); err != nil {
		t.Fatal(err)
	}
	viaCLI = shape(budget.PathKillRepo(appSlug))
	f.db.Set(budget.PathKillRepo(appSlug), map[string]any{"on": true, "by": "x", "at": 1})
	watch.Execute(context.Background(), db, watch.Request{Kind: watch.ResumeRepo, Project: "aurora", Target: watch.Target{Slug: appSlug, Name: "acme/app"}, Reason: "ok"}, "someone@example.com")
	if viaTUI := shape(budget.PathKillRepo(appSlug)); !reflect.DeepEqual(viaCLI, viaTUI) {
		t.Fatalf("resume: CLI wrote %v, TUI wrote %v", viaCLI, viaTUI)
	}
	// And the refusal is the same text.
	f.db.DenyNext(1)
	o = watch.Execute(context.Background(), db, watch.Request{Kind: watch.KillAll, Project: "aurora", Reason: "r"}, "me")
	if o.Notice != budget.AdminDeniedText("aurora") {
		t.Fatalf("%q", o.Notice)
	}
}

// The embedded table carries no placeholder row for a provider model any
// more: the built-in provider price catalog prices those (P6), and with no
// project config no route claims one, so the printed table holds no provider
// model at all — and no row is marked unverified. The catalog's own
// provenance in this table is the CATALOG column's job.
func TestPricesHasNoPlaceholderRow(t *testing.T) {
	const key = "sk-or-v1-testkeyshouldneverprint"
	t.Setenv("OPENROUTER_API_KEY", key)
	isolateProjects(t, t.TempDir())
	out, _, err := execute(t, "budget", "prices")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "UNVERIFIED") || strings.Contains(out, "deepseek/") {
		t.Fatalf("a placeholder row is still printed:\n%s", out)
	}
	var sonnet string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "claude-sonnet-5-5") {
			sonnet = l
		}
	}
	if !strings.Contains(sonnet, "yes") || strings.Contains(sonnet, "UNVERIFIED") {
		t.Fatalf("a checked row is marked unverified:\n%s", out)
	}
	js, _, err := execute(t, "budget", "prices", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Models []priceRow `json:"models"`
	}
	if err := json.Unmarshal([]byte(js), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Models) == 0 {
		t.Fatalf("no rows: %s", js)
	}
	for _, m := range doc.Models {
		if m.Unverified {
			t.Errorf("%s is marked unverified", m.ID)
		}
	}
	if strings.Contains(out+js, key) {
		t.Fatal("the provider key is in the output")
	}
}

func TestPricesOverrideClearsUnverified(t *testing.T) {
	newBudgetFixture(t, "budget: { mode: observe, per_run_usd: 5, rtdb_url: http://127.0.0.1:9 }\n"+
		"model_prices:\n  deepseek/deepseek-v4-flash: { input_per_m: 0.2, output_per_m: 0.8, cache_read: 0.1 }\n")
	out, _, err := execute(t, "budget", "prices")
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "deepseek/deepseek-v4-flash") {
			if strings.Contains(l, "UNVERIFIED") || !strings.Contains(l, "local override") || !strings.Contains(l, "yes") {
				t.Fatalf("an owner price should be verified and marked local:\n%s", l)
			}
			return
		}
	}
	t.Fatalf("no deepseek row:\n%s", out)
}

// catalogRouteYAML is a project config's route block that the built-in
// provider price catalog prices (OpenRouter, claiming the DeepSeek rows).
const catalogRouteYAML = `providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    models: ["deepseek/*"]
    allow_data_to: ["acme/app"]
`

// shippedCatalog is the price catalog this repository ships, embedded in the
// binary under test.
func shippedCatalog(t *testing.T) *catalog.File {
	t.Helper()
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	return cat
}

// pinCatalogClock pins the CLI's clock to the shipped catalog's own
// checked_at plus d, and puts it back when the test ends. A test about the
// catalog measures from the file's own date and never from today's: the
// catalog's 30 days (catalog.StaleAfter) decide which end of a tier it
// charges, so a test that read the wall clock would assert the cheapest end
// today and start failing on a date no code change marked.
func pinCatalogClock(t *testing.T, cat *catalog.File, d time.Duration) {
	t.Helper()
	checked, err := time.Parse("2006-01-02", cat.CheckedAt)
	if err != nil {
		t.Fatal(err)
	}
	old := budgetNow
	budgetNow = func() time.Time { return checked.Add(d) }
	t.Cleanup(func() { budgetNow = old })
}

// TestBudgetPricesCatalogColumn: a row the built-in provider price catalog
// priced says so in its own column — the tier and the end of that tier's
// range charged — and its note carries the endpoint count and the catalog's
// own date, so the two sources in one table are never read as one (design
// §7, Task 5 step 1).
func TestBudgetPricesCatalogColumn(t *testing.T) {
	cat := shippedCatalog(t)
	pinCatalogClock(t, cat, 24*time.Hour)
	entry := cat.Models["deepseek/deepseek-v4.1-flash"]
	newBudgetFixture(t, "budget: { mode: observe, per_run_usd: 5, rtdb_url: http://127.0.0.1:9 }\n"+catalogRouteYAML)
	out, _, err := execute(t, "budget", "prices")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "CATALOG") {
		t.Fatalf("no CATALOG column:\n%s", out)
	}
	var row string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "deepseek/deepseek-v4.1-flash") {
			row = l
		}
	}
	if row == "" {
		t.Fatalf("the route's model has no row:\n%s", out)
	}
	// The counts and the date are the catalog file's own, read here rather
	// than written down: regenerating it is a documented release step
	// (docs/release.md), and this test is about the column, not about
	// which models the last fetch found.
	for _, want := range []string{"zdr/cheapest", fmt.Sprintf("%d/%d", entry.ZDREndpoints, entry.Endpoints), cat.CheckedAt} {
		if !strings.Contains(row, want) {
			t.Errorf("the catalog row lacks %q:\n%s", want, row)
		}
	}
	// The numbers behind the range are on the surface as well: what the
	// dearest endpoint of the tier prices and how many times the input price
	// charged that is — the size of the undercharge a cheapest row leaves
	// (design D6, P11). Read from the file, like the counts above.
	inMax := zdrPrice(t, entry, "input_per_m")
	outMax := zdrPrice(t, entry, "output_per_m")
	if want := fmt.Sprintf("can be billed $%g/M in and $%g/M out", inMax, outMax); !strings.Contains(row, want) {
		t.Errorf("the catalog row does not size the undercharge (%q):\n%s", want, row)
	}
	// A Claude row is not catalog-priced: the column says so with a dash,
	// and the catalog's provenance is not on it.
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "claude-sonnet-5-5") && !strings.Contains(l, "—") {
			t.Errorf("a Claude row claims a catalog:\n%s", l)
		}
	}
	js, _, err := execute(t, "budget", "prices", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		CatalogSource    string `json:"catalog_source"`
		CatalogCheckedAt string `json:"catalog_checked_at"`
		CatalogStale     bool   `json:"catalog_stale"`
		Models           []struct {
			ID      string `json:"id"`
			Catalog *struct {
				Tier              string  `json:"tier"`
				Bound             string  `json:"bound"`
				Endpoints         int     `json:"endpoints"`
				ZdrEndpoints      int     `json:"zdr_endpoints"`
				Stale             bool    `json:"stale"`
				TierInputMaxPerM  float64 `json:"tier_input_max_per_m"`
				TierOutputMaxPerM float64 `json:"tier_output_max_per_m"`
			} `json:"catalog"`
		} `json:"models"`
	}
	if err := json.Unmarshal([]byte(js), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.CatalogCheckedAt != cat.CheckedAt || doc.CatalogStale || !strings.Contains(doc.CatalogSource, "openrouter.ai") {
		t.Fatalf("the catalog summary is %+v", doc)
	}
	found := false
	for _, m := range doc.Models {
		if m.Catalog == nil {
			continue
		}
		if m.ID == "deepseek/deepseek-v4.1-flash" {
			found = true
			if m.Catalog.Tier != "zdr" || m.Catalog.Bound != "min" || m.Catalog.Endpoints != entry.Endpoints || m.Catalog.ZdrEndpoints != entry.ZDREndpoints || m.Catalog.Stale {
				t.Errorf("%s's catalog row is %+v", m.ID, m.Catalog)
			}
			if m.Catalog.TierInputMaxPerM != inMax || m.Catalog.TierOutputMaxPerM != outMax {
				t.Errorf("%s's range is %v/%v, want the file's %v/%v", m.ID,
					m.Catalog.TierInputMaxPerM, m.Catalog.TierOutputMaxPerM, inMax, outMax)
			}
		}
	}
	if !found {
		t.Fatalf("no catalog-priced row in the JSON: %s", js)
	}
}

// TestBudgetPricesListsRefusedModels: a route may claim a model the catalog
// cannot price — qwen/qwen3.8-flash has no ZDR-eligible endpoint, so a
// require_zdr route claims it and gets no row — and the owner is told which
// model and why rather than left with a table that silently omits it: the
// model is in the JSON's refused list and warned about on stderr (design D9,
// P8). Nothing is refused here: a run that never names the model charges
// nothing for it, and a pin on one is refused at bootstrap.
func TestBudgetPricesListsRefusedModels(t *testing.T) {
	const qwen = "qwen/qwen3.8-flash"
	cat := shippedCatalog(t)
	pinCatalogClock(t, cat, 24*time.Hour)
	if len(cat.Models[qwen].ZDR) != 0 {
		t.Fatalf("the shipped catalog gives %s a zdr tier: this test needs it refused", qwen)
	}
	newBudgetFixture(t, "budget: { mode: observe, per_run_usd: 5, rtdb_url: http://127.0.0.1:9 }\n"+
		strings.Replace(catalogRouteYAML, `["deepseek/*"]`, `["qwen/*"]`, 1))
	out, errOut, err := execute(t, "budget", "prices")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, qwen) {
		t.Errorf("the refused model has a row:\n%s", out)
	}
	if !strings.Contains(errOut, qwen) || !strings.Contains(errOut, "require_zdr: false") {
		t.Errorf("stderr does not say which model has no row and why:\n%s", errOut)
	}
	js, _, err := execute(t, "budget", "prices", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Refused []struct {
			ID     string `json:"id"`
			Reason string `json:"reason"`
		} `json:"refused"`
	}
	if err := json.Unmarshal([]byte(js), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Refused) != 1 || doc.Refused[0].ID != qwen {
		t.Fatalf("refused = %+v, want the one model the route claims and the catalog cannot price", doc.Refused)
	}
	for _, want := range []string{"require_zdr: false", "model_prices"} {
		if !strings.Contains(doc.Refused[0].Reason, want) {
			t.Errorf("the refusal does not name %q: %q", want, doc.Refused[0].Reason)
		}
	}
}

// zdrPrice is one dimension's dearest price in the shipped entry's zdr tier,
// as a number, so a test about the numbers reads them from the file rather
// than writing them down.
func zdrPrice(t *testing.T, e catalog.Entry, dim string) float64 {
	t.Helper()
	v, err := strconv.ParseFloat(e.ZDR[dim].Max, 64)
	if err != nil {
		t.Fatalf("the shipped file's zdr/%s max is not a number: %v", dim, err)
	}
	return v
}

// TestBudgetPricesStaleCatalogWarns: past the catalog's own 30 days the
// catalog-priced rows charge the dearest end of their tier and the command
// says so, naming the catalog (not the Claude table) and the command that
// regenerates it (design §7, P7).
func TestBudgetPricesStaleCatalogWarns(t *testing.T) {
	newBudgetFixture(t, "budget: { mode: observe, per_run_usd: 5, rtdb_url: http://127.0.0.1:9 }\n"+catalogRouteYAML)
	cat := shippedCatalog(t)

	// Six days after the catalog was checked, well inside its window.
	pinCatalogClock(t, cat, 6*24*time.Hour)
	out, errOut, err := execute(t, "budget", "prices")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errOut, "gen-price-catalog") {
		t.Fatalf("a 6-day-old catalog was called stale: %q", errOut)
	}
	if !strings.Contains(out, "zdr/cheapest") {
		t.Fatalf("the fresh row is not at the cheapest end:\n%s", out)
	}

	// The window is 30 days (catalog.StaleAfter): a day past it is stale.
	pinCatalogClock(t, cat, 31*24*time.Hour)
	out, errOut, err = execute(t, "budget", "prices")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"provider price catalog", "older than 30 days", "scripts/gen-price-catalog.sh"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("the stale warning lacks %q: %q", want, errOut)
		}
	}
	if !strings.Contains(out, "zdr/highest") {
		t.Errorf("a stale row does not charge the dearest end:\n%s", out)
	}
	js, _, err := execute(t, "budget", "prices", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(js, `"catalog_stale": true`) {
		t.Errorf("the JSON does not mark the catalog stale: %s", js)
	}
}

// TestBudgetShowWarnsAtEightyPercent: once a daily counter passes 80% of its
// cap, show prints one warning line for that cap — the repository's and the
// project's — and 79.9% warns not at all (R5). Nothing is refused: it is a
// warning beside the cap that keeps applying.
func TestBudgetShowWarnsAtEightyPercent(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.db.Set("config/mode", "enforce")
	seedCaps(f, 100, 20)
	f.db.Set(budget.PathCapsDefaults, map[string]any{"repoDailyMicros": 50 * usd1})
	d := today()
	// The config's one repository is the only one the database holds, so its
	// counter is the project's too: figures that do not add up warn about that
	// instead, and the test is not about that warning.
	set := func(counted int64) {
		f.db.Set(budget.PathSpendGlobal(d), map[string]any{"counted": counted})
		f.db.Set(budget.PathSpendRepo(d, appSlug), map[string]any{"counted": counted})
	}
	warnings := func(t *testing.T) []string {
		t.Helper()
		out, _, err := execute(t, "budget", "show", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Warnings []string `json:"warnings"`
		}
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return doc.Warnings
	}

	// 79.9% of the repository's $50 day: below the share, no line at all.
	set(39_950_000)
	if w := warnings(t); len(w) != 0 {
		t.Fatalf("79.9%% of a cap warned: %q", w)
	}
	if out, _, err := execute(t, "budget", "show"); err != nil || strings.Contains(out, "note:") {
		t.Fatalf("79.9%% printed a note: %v\n%s", err, out)
	}

	// Exactly the share warns, for the repository's $50 cap; the project's $100
	// one is at 40% and says nothing.
	set(40_000_000)
	w := warnings(t)
	if len(w) != 1 || !strings.Contains(w[0], "acme/app") || !strings.Contains(w[0], "$40.00 of $50.00") {
		t.Fatalf("warnings at 80%% of the repository's cap = %q", w)
	}

	// Past both caps' share: one line each, the project's included.
	set(82_000_000)
	w = warnings(t)
	if len(w) != 2 || !strings.Contains(strings.Join(w, "\n"), "the project's daily cap") ||
		!strings.Contains(strings.Join(w, "\n"), "$82.00 of $100.00") {
		t.Fatalf("warnings at 82%% = %q", w)
	}
	out, _, err := execute(t, "budget", "show")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out, "note: "); n != 2 {
		t.Fatalf("show printed %d notes, want 2:\n%s", n, out)
	}
}

// TestBudgetShowNotesAZeroCap: a daily cap of zero is a cap, not an unset one
// — the rules refuse every write against it, so it is 100% counted, and watch
// draws it at 100% (watch's build.go bar) — so show notes it too, instead of
// watch alone saying "100%". The project's own cap is not zero, so it draws no
// note: the line is the zero cap's.
func TestBudgetShowNotesAZeroCap(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.db.Set("config/mode", "enforce")
	seedCaps(f, 100, 20)
	f.db.Set(budget.PathCapsRepo(appSlug), map[string]any{"repo": "acme/app", "dailyMicros": 0})
	out, _, err := execute(t, "budget", "show")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "note: acme/app's daily cap: $0.00 of $0.00 counted (100.0%)") {
		t.Fatalf("a zero cap drew no note:\n%s", out)
	}
	if n := strings.Count(out, "note: "); n != 1 {
		t.Fatalf("show printed %d notes, want 1:\n%s", n, out)
	}
}

// TestBudgetShowAllNamesAnUnnamedRepositoryFromTheRegistry: with --all, a
// repository the config does not name takes its name from the registry (the
// agents loop's `if rs.Repo == ""` fallback), and the 80% note must be
// computed after that loop so it calls the repository what printShow's heading
// does — the registry's owner/name, not the database key's slug.
func TestBudgetShowAllNamesAnUnnamedRepositoryFromTheRegistry(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.db.Set("config/mode", "enforce")
	seedCaps(f, 1000, 20) // the project's own cap is far from its share
	f.db.Set(budget.PathCapsDefaults, map[string]any{"repoDailyMicros": 50 * usd1})
	d := today()
	lib := mustSlug("github", "acme/lib")
	// The project's counter matches its one repository's, so the figures that
	// do not add up draw no warning of their own here.
	f.db.Set(budget.PathSpendGlobal(d), map[string]any{"counted": 45 * usd1})
	f.db.Set(budget.PathSpendRepo(d, lib), map[string]any{"counted": 45 * usd1}) // 90% of the default
	f.db.Set(budget.PathAgent(lib, "20261002-090000-bbbb"), map[string]any{
		"repo": "acme/lib", "stage": "coding", "updatedAt": time.Now().UnixMilli(),
	})
	out, _, err := execute(t, "budget", "show", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "repository acme/lib (") {
		t.Fatalf("the heading does not name the registry's repository:\n%s", out)
	}
	var note string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "note: ") && strings.Contains(line, "daily cap:") {
			note = line
		}
	}
	if !strings.Contains(note, "acme/lib's daily cap: $45.00 of $50.00 counted") {
		t.Fatalf("the note does not call the repository what the heading does: %q\n%s", note, out)
	}
	if strings.Contains(note, lib) {
		t.Fatalf("the note names the slug %q, not the registry's repository: %q", lib, note)
	}
}
