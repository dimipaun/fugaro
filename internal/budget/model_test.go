package budget

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/pricing"
)

func TestKeyEscaping(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"aurora", "aurora"},
		{"a.b", "a%2Eb"},
		{"a/b", "a%2Fb"},
		{"a$b#c[d]e", "a%24b%23c%5Bd%5De"},
		{"100%", "100%25"},
		{"%2E", "%252E"}, // a literal %2E must not collide with an escaped dot
		{"", ""},
		{"x\ny", "x%0Ay"},
	} {
		got := Key(tc.in)
		if got != tc.want {
			t.Errorf("Key(%q) = %q, want %q", tc.in, got, tc.want)
		}
		back, err := Unkey(got)
		if err != nil || back != tc.in {
			t.Errorf("Unkey(Key(%q)) = %q, %v", tc.in, back, err)
		}
	}
	for _, k := range []string{"a.b", "a/b", "$", "#", "[", "]", "a\x00b", "a\x7fb"} {
		if strings.ContainsAny(Key(k), "./$#[]") || strings.ContainsFunc(Key(k), func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			t.Errorf("Key(%q) = %q still holds a character RTDB forbids", k, Key(k))
		}
	}
	if _, err := Unkey("a%2"); err == nil {
		t.Error("Unkey accepted a truncated escape")
	}
	if _, err := Unkey("a%zz"); err == nil {
		t.Error("Unkey accepted a bad escape")
	}
}

func TestKeyInjectiveProperty(t *testing.T) {
	seen := map[string]string{}
	alphabet := []string{"a", ".", "%", "2", "E", "/", "$"}
	var walk func(prefix string, depth int)
	walk = func(prefix string, depth int) {
		k := Key(prefix)
		if prev, ok := seen[k]; ok && prev != prefix {
			t.Fatalf("%q and %q both escape to %q", prev, prefix, k)
		}
		seen[k] = prefix
		if depth == 0 {
			return
		}
		for _, a := range alphabet {
			walk(prefix+a, depth-1)
		}
	}
	walk("", 4)
}

func TestDayNumbers(t *testing.T) {
	utc := func(y int, m time.Month, d, h, mi, s, ns int) time.Time {
		return time.Date(y, m, d, h, mi, s, ns, time.UTC)
	}
	for _, tc := range []struct {
		name string
		now  time.Time
		day  int64
		date string
	}{
		{"epoch", utc(1970, 1, 1, 0, 0, 0, 0), 0, "1970-01-01"},
		{"last ms of day 0", utc(1970, 1, 1, 23, 59, 59, 999e6), 0, "1970-01-01"},
		{"first ms of day 1", utc(1970, 1, 2, 0, 0, 0, 0), 1, "1970-01-02"},
		{"boundary minus 1ns", utc(2026, 10, 1, 23, 59, 59, 999999999), 20727, "2026-10-01"},
		{"boundary", utc(2026, 10, 2, 0, 0, 0, 0), 20728, "2026-10-02"},
		{"leap day", utc(2024, 2, 29, 12, 0, 0, 0), 19782, "2024-02-29"},
		{"after leap day", utc(2024, 3, 1, 0, 0, 0, 0), 19783, "2024-03-01"},
		{"non-leap century", utc(2100, 3, 1, 0, 0, 0, 0), 47541, "2100-03-01"},
		{"before the epoch", utc(1969, 12, 31, 23, 59, 59, 0), -1, "1969-12-31"},
		{"a non-UTC zone is converted", time.Date(2026, 10, 2, 1, 0, 0, 0, time.FixedZone("x", 2*3600)), 20727, "2026-10-01"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Day(tc.now); got != tc.day {
				t.Errorf("Day = %d, want %d", got, tc.day)
			}
			if got := DayDate(tc.day); got != tc.date {
				t.Errorf("DayDate = %s, want %s", got, tc.date)
			}
			if got := DayKey(tc.day); got != strconv.FormatInt(tc.day, 10) {
				t.Errorf("DayKey = %q", got)
			}
		})
	}
	// The rules' TODAY: now_ms - now_ms % 86400000, / 86400000.
	for _, ms := range []int64{0, 86399999, 86400000, 1790899200000 - 1, 1790899200000} {
		now := time.UnixMilli(ms)
		if got, want := Day(now), ms/86400000; got != want {
			t.Errorf("Day(%d ms) = %d, want %d", ms, got, want)
		}
	}
}

func TestPaths(t *testing.T) {
	for got, want := range map[string]string{
		PathCapsGlobal:                   "config/caps/global",
		PathCapsDefaults:                 "config/caps/defaults",
		PathLimits:                       "config/limits",
		PathMode:                         "config/mode",
		PathKillGlobal:                   "config/kill/global",
		PathCapsRepo("aurora"):           "config/caps/repos/aurora",
		PathKillRepo("a.b"):              "config/kill/repos/a%2Eb",
		PathRun("a.b", "r1"):             "runs/a%2Eb/r1",
		PathSpendGlobal(20728):           "spend/20728/global",
		PathSpendRepo(20728, "a.b"):      "spend/20728/repos/a%2Eb",
		PathSpendRun(20728, "a.b", "r1"): "spend/20728/runs/a%2Eb/r1",
		PathSpendMeta(20728):             "spend/20728/meta",
		PathAgent("a.b", "r1"):           "agents/a%2Eb/r1",
		PathOutcome(20728, "a.b", "r1"):  "outcomes/20728/a%2Eb/r1",
		PathMark:                         "fugaro/mark",
		PathProject:                      "fugaro/project",
	} {
		if got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
	}
}

func TestDocumentsJSON(t *testing.T) {
	// Absent caps stay absent (nil), they never become 0: a zero cap and a
	// missing cap are different things to the rules.
	b, err := json.Marshal(GlobalCaps{})
	if err != nil || string(b) != "{}" {
		t.Fatalf("empty GlobalCaps = %s, %v", b, err)
	}
	d := pricing.Micros(60_000_000)
	var rc RepoCaps
	if err := json.Unmarshal([]byte(`{"dailyMicros":60000000,"repo":"aurora/web"}`), &rc); err != nil {
		t.Fatal(err)
	}
	if rc.DailyMicros == nil || *rc.DailyMicros != d || rc.PerRunMicros != nil || rc.Repo != "aurora/web" {
		t.Fatalf("RepoCaps = %+v", rc)
	}
	var run RunLedger
	if err := json.Unmarshal([]byte(`{"reserved":5000000,"released":1000000,"spent":2000000}`), &run); err != nil {
		t.Fatal(err)
	}
	if run.Outstanding() != 4_000_000 || run.Unsettled() != 2_000_000 {
		t.Fatalf("Outstanding/Unsettled = %d/%d", run.Outstanding(), run.Unsettled())
	}
	b, _ = json.Marshal(Kill{On: true, By: "dimi@example.invalid", At: 1, Reason: "x"})
	if string(b) != `{"on":true,"by":"dimi@example.invalid","at":1,"reason":"x"}` {
		t.Fatalf("Kill = %s", b)
	}
}

func TestEffectiveCaps(t *testing.T) {
	m := func(v int64) *pricing.Micros { x := pricing.Micros(v); return &x }
	c := Caps{
		Global:   &GlobalCaps{DailyMicros: m(150), PerRunMicros: m(20)},
		Defaults: &DefaultCaps{RepoDailyMicros: m(60), RepoPerRunMicros: m(10)},
	}
	if v, ok := c.RepoDaily(); !ok || v != 60 {
		t.Errorf("RepoDaily falls back to the default: %d %v", v, ok)
	}
	if v, ok := c.PerRun(); !ok || v != 10 {
		t.Errorf("PerRun = min(default 10, global 20): %d %v", v, ok)
	}
	c.Repo = &RepoCaps{DailyMicros: m(40), PerRunMicros: m(30)}
	if v, _ := c.RepoDaily(); v != 40 {
		t.Errorf("repo node wins over defaults: %d", v)
	}
	if v, _ := c.PerRun(); v != 20 {
		t.Errorf("PerRun = min(repo 30, global 20): %d", v)
	}
	c.Global.PerRunMicros = nil
	if _, ok := c.PerRun(); ok {
		t.Error("PerRun with no global per-run cap must be unset (the rules read null and deny)")
	}
	if _, ok := (Caps{}).RepoDaily(); ok {
		t.Error("no caps at all is no cap")
	}
	if _, ok := (Caps{}).GlobalDaily(); ok {
		t.Error("no global cap is no cap")
	}
}

func TestPathsRejectEmptySegments(t *testing.T) {
	for name, f := range map[string]func(){
		"PathCapsRepo":  func() { PathCapsRepo("") },
		"PathKillRepo":  func() { PathKillRepo("") },
		"PathRun slug":  func() { PathRun("", "r") },
		"PathRun run":   func() { PathRun("s", "") },
		"PathSpendRepo": func() { PathSpendRepo(1, "") },
		"PathSpendRun":  func() { PathSpendRun(1, "s", "") },
		"PathAgent":     func() { PathAgent("", "r") },
		"PathOutcome":   func() { PathOutcome(1, "s", "") },
		"PathByModel":   func() { PathByModel(1, "s", "") },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s accepted an empty segment (it would collapse onto another node)", name)
				}
			}()
			f()
		}()
	}
}

func TestByModelKeysAreEscaped(t *testing.T) {
	if got := ModelKey("claude-3.5-sonnet"); got != "claude-3%2E5-sonnet" {
		t.Fatalf("ModelKey = %q", got)
	}
	if got := PathByModel(20728, "a.b", "gemini-2.5-pro"); got != "spend/20728/repos/a%2Eb/byModel/gemini-2%2E5-pro" {
		t.Fatalf("PathByModel = %q", got)
	}
	// Models("") reads the wire form back to model names; dotted and
	// percent-bearing names stay distinct.
	c := Counters{ByModel: map[string]ModelUse{
		ModelKey("claude-3.5"):   {Micros: 1},
		ModelKey("claude-3%2E5"): {Micros: 2},
		ModelKey("vertex@2025"):  {Micros: 3},
	}}
	got := c.Models()
	if len(got) != 3 || got["claude-3.5"].Micros != 1 || got["claude-3%2E5"].Micros != 2 || got["vertex@2025"].Micros != 3 {
		t.Fatalf("Models = %+v", got)
	}
	for _, m := range []string{"x", "a.b", "a/b", "a$b", "a[1]"} {
		if strings.ContainsAny(ModelKey(m), "./$#[]") {
			t.Errorf("ModelKey(%q) = %q has a forbidden character", m, ModelKey(m))
		}
	}
}
