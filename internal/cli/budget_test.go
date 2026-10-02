package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

// budgetFixture is a project config whose budget backend is the RTDB fake.
type budgetFixture struct {
	db *gcpfake.RTDB
}

const (
	usd1   = int64(1_000_000)
	nodeCG = "config/caps/global"
)

func newBudgetFixture(t *testing.T, budgetYAML string) *budgetFixture {
	t.Helper()
	f := &budgetFixture{db: gcpfake.NewRTDB(t)}
	dir := t.TempDir()
	path := isolateProjects(t, dir)
	if budgetYAML == "" {
		budgetYAML = "budget: { mode: observe, per_run_usd: 5, rtdb_url: " + f.db.URL + " }\n"
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
	return f
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
			Slug        string   `json:"slug"`
			Repo        string   `json:"repo"`
			CountedUSD  float64  `json:"counted_usd"`
			DailyCapUSD *float64 `json:"daily_cap_usd"`
			CapSource   string   `json:"daily_cap_source"`
			HeadroomUSD *float64 `json:"headroom_usd"`
			Kill        *struct {
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
	// headroom is the smaller of 40-25 and 100-30.
	if r.Slug != appSlug || r.Repo != "acme/app" || r.CountedUSD != 25 || r.DailyCapUSD == nil || *r.DailyCapUSD != 40 ||
		r.CapSource != "default" || r.HeadroomUSD == nil || *r.HeadroomUSD != 15 {
		t.Errorf("repo = %+v", r)
	}
	if r.Kill == nil || r.Kill.By != "boss@example.com" || r.Kill.Reason != "runaway" {
		t.Errorf("kill = %+v", r.Kill)
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
	if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "confirmation") {
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

	budgetNow = func() time.Time { return time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC) }
	out, errOut, err := execute(t, "budget", "prices")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "claude-sonnet-5-5") || !strings.Contains(out, "2026-09-30") || !strings.Contains(out, "pricing") {
		t.Fatalf("table:\n%s", out)
	}
	if strings.Contains(errOut, "older") {
		t.Fatalf("a fresh table was called stale: %q", errOut)
	}

	budgetNow = func() time.Time { return time.Date(2027, 1, 15, 0, 0, 0, 0, time.UTC) }
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
	if err := json.Unmarshal([]byte(js), &doc); err != nil || doc.CheckedAt != "2026-09-30" || !doc.Stale || len(doc.Models) < 3 {
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

func TestBudgetRefusesWithoutFirebaseConfig(t *testing.T) {
	newBudgetFixture(t, "budget: { mode: observe, per_run_usd: 5 }\n")
	for _, args := range [][]string{{"show"}, {"set", "--global", "--daily", "1", "--yes"}, {"kill", "--all", "--yes"}, {"resume", "--all", "--yes"}} {
		_, _, err := execute(t, append([]string{"budget"}, args...)...)
		if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "init --firebase") {
			t.Errorf("%v: err = %v", args, err)
		}
	}
	// No budget block at all.
	newBudgetFixture(t, " \n")
	if _, _, err := execute(t, "budget", "show"); err == nil || !strings.Contains(err.Error(), "init --firebase") {
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
