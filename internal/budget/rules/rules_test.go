package rules

import (
	"bytes"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
)

const goldenPath = "testdata/rules.golden.json"

// TestRulesGolden pins the generated rules. Regenerate on purpose with
// UPDATE_GOLDEN=1 go test ./internal/budget/rules and review the diff: it is
// security code.
func TestRulesGolden(t *testing.T) {
	got, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("generated rules differ from %s (UPDATE_GOLDEN=1 regenerates; review the diff)", goldenPath)
	}
	again, _ := Generate()
	if !bytes.Equal(got, again) {
		t.Fatal("Generate is not deterministic")
	}
}

// rulesTree decodes the generated rules into maps (order is irrelevant here).
func rulesTree(t *testing.T) map[string]any {
	t.Helper()
	b, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("generated rules are not JSON: %v", err)
	}
	if len(doc) != 1 || doc["rules"] == nil {
		t.Fatalf("top level must be exactly {\"rules\": ...}, got %v", keys(doc))
	}
	return doc["rules"]
}

func keys[V any](m map[string]V) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

// walk visits every location of the rule tree with its path and attributes.
func walk(n map[string]any, path []string, fn func(path []string, attrs map[string]any)) {
	attrs := map[string]any{}
	for k, v := range n {
		if strings.HasPrefix(k, ".") {
			attrs[k] = v
		}
	}
	fn(path, attrs)
	for k, v := range n {
		if m, ok := v.(map[string]any); ok && !strings.HasPrefix(k, ".") {
			walk(m, append(append([]string(nil), path...), k), fn)
		}
	}
}

// macro words that must not survive generation.
var macroWord = regexp.MustCompile(`\b[A-Z][A-Z_]{2,}\b|\b[OND]\(`)

func TestGeneratedRulesParse(t *testing.T) {
	rules := rulesTree(t)
	count := 0
	walk(rules, nil, func(path []string, attrs map[string]any) {
		for k, v := range attrs {
			switch k {
			case ".read", ".write", ".validate":
			default:
				t.Errorf("/%s: unexpected attribute %s", strings.Join(path, "/"), k)
			}
			switch e := v.(type) {
			case bool:
			case string:
				count++
				if strings.TrimSpace(e) == "" {
					t.Errorf("/%s %s is empty", strings.Join(path, "/"), k)
				}
				if strings.Count(e, "(") != strings.Count(e, ")") || strings.Count(e, "'")%2 != 0 {
					t.Errorf("/%s %s has unbalanced parentheses or quotes: %s", strings.Join(path, "/"), k, e)
				}
			default:
				t.Errorf("/%s %s is %T", strings.Join(path, "/"), k, v)
			}
		}
	})
	if count < 50 {
		t.Fatalf("only %d rule expressions generated", count)
	}
	// Default deny at the root: no read or write grant above the locations.
	if rules[".read"] != false || rules[".write"] != false {
		t.Fatalf("root must deny by default, got read=%v write=%v", rules[".read"], rules[".write"])
	}
}

// TestMacrosExpandOnce: no macro text survives, and expansion is stable (the
// output holds nothing the expander would expand again).
func TestMacrosExpandOnce(t *testing.T) {
	walk(rulesTree(t), nil, func(path []string, attrs map[string]any) {
		for k, v := range attrs {
			s, ok := v.(string)
			if !ok {
				continue
			}
			if m := macroWord.FindString(s); m != "" {
				t.Errorf("/%s %s still holds the macro %q", strings.Join(path, "/"), k, m)
			}
		}
	})
	for _, tc := range []struct {
		in    string
		depth int
	}{
		{"NEW", 3}, {"RUN", 2}, {"D(spend/TODAY/runs/$slug/$run/reserved)", 4}, {"COUNTERS_FOLLOW", 6}, {"RUNDELTA", 5},
	} {
		once, err := expand(tc.in, tc.depth)
		if err != nil {
			t.Fatal(err)
		}
		twice, err := expand(once, tc.depth)
		if err != nil {
			t.Fatal(err)
		}
		if once != twice {
			t.Errorf("%s: expanding the expansion changes it:\n%s\n%s", tc.in, once, twice)
		}
	}
	if _, err := expand("NOSUCHMACRO", 1); err == nil {
		t.Error("an unknown macro must fail generation, not pass through")
	}
	if _, err := expand("N(a/b'c)", 1); err == nil {
		t.Error("a quote in a path segment must fail generation")
	}
}

// TestNewDataChainsReachTheRoot: N() and D() climb exactly as many parents as
// the location is deep, or they would read the wrong node.
func TestNewDataChainsReachTheRoot(t *testing.T) {
	for depth := 1; depth <= 6; depth++ {
		s, err := expand("N(x)", depth)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Count(s, ".parent()"); got != 2*depth {
			t.Errorf("depth %d: %d parent() calls in %s, want %d", depth, got, s, 2*depth)
		}
	}
}

// TestWritesOnlyAtNumericLeaves pins the structural security property of the
// ledgers and counters: no .write above a leaf, so a whole-node write or a
// delete of a field is denied (validation does not run on deletes).
func TestWritesOnlyAtNumericLeaves(t *testing.T) {
	rules := rulesTree(t)
	guarded := map[string]bool{"runs": true, "spend": true}
	walk(rules, nil, func(path []string, attrs map[string]any) {
		if len(path) == 0 {
			return
		}
		w, has := attrs[".write"]
		where := "/" + strings.Join(path, "/")
		switch path[0] {
		case "config", "fugaro":
			if has && w != false {
				t.Errorf("%s must never be writable by a run: .write = %v", where, w)
			}
		case "runs", "spend":
			if !guarded[path[0]] {
				return
			}
			leaf := path[len(path)-1]
			isLeaf := map[string]bool{"reserved": true, "released": true, "spent": true, "notional": true, "overrun": true, "tokens": true,
				"exp": true, "counted": true, "calls": true, "micros": true, "in": true, "out": true, "cr": true, "cw": true}[leaf]
			if has && !isLeaf {
				t.Errorf("%s has a .write above a leaf: %v", where, w)
			}
			if isLeaf {
				ws, _ := w.(string)
				if !strings.Contains(ws, "newData.exists()") {
					t.Errorf("%s: a leaf .write must refuse deletes (newData.exists()): %v", where, w)
				}
				if _, ok := attrs[".validate"]; !ok {
					t.Errorf("%s: a leaf needs a .validate", where)
				}
			}
		}
	})
}

// TestPerRunOK pins the lease's per-run comparison (design D6): one boolean
// reading all three sides, so that a side that is not set does not bound the
// run -- the global cap is the fallback of a repository that has none, the
// default is the fallback of a global one -- and the both-unset branch is
// false, so the rules deny (fail closed).
func TestPerRunOK(t *testing.T) {
	ok, err := perRunOK(4) // the depth of runs/$slug/$run/reserved
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ok, ": false)") {
		t.Errorf("no false branch: with no per-run cap anywhere the rules must deny: %s", ok)
	}
	if !strings.Contains(ok, ": true)") {
		t.Errorf("no true branch: a repository with a cap of its own and no global one must pass: %s", ok)
	}
	for _, side := range []string{
		"root.child('config/caps/repos').child($slug).child('perRunMicros')",
		"root.child('config/caps/defaults/repoPerRunMicros')",
		"root.child('config/caps/global/perRunMicros')",
	} {
		if !strings.Contains(ok, side) {
			t.Errorf("the comparison does not read %s: %s", side, ok)
		}
	}
	if strings.Count(ok, "(") != strings.Count(ok, ")") || strings.Count(ok, "'")%2 != 0 {
		t.Errorf("unbalanced parentheses or quotes: %s", ok)
	}
	if m := macroWord.FindString(ok); m != "" {
		t.Errorf("the expansion still holds the macro %q: %s", m, ok)
	}
	twice, err := expand(ok, 4)
	if err != nil || twice != ok {
		t.Errorf("expanding the expansion changes it (%v):\n%s\n%s", err, ok, twice)
	}

	// It is the whole enforce branch of the lease's reserved validator: the
	// rules compare the per-run caps nowhere else.
	var validate string
	walk(rulesTree(t), nil, func(path []string, attrs map[string]any) {
		if strings.Join(path, "/") == "runs/$slug/$run/reserved" {
			validate, _ = attrs[".validate"].(string)
		}
	})
	if validate == "" {
		t.Fatal("the lease's reserved validator was not found")
	}
	if !strings.Contains(validate, "(!(root.child('config/mode').val() == 'enforce') || "+ok+")") {
		t.Errorf("the reserved validator's enforce branch is not perRunOK:\n%s", validate)
	}
}

// TestGoMirrorOfThePerRunComparison: perRunOK's branches, written out in Go
// and checked against budget.Caps.PerRun for every combination of the three
// sides and a range of amounts. The emulator suite and the parity test prove
// the rules agree with the client, but they need the Firebase emulator; this
// runs everywhere and pins the intent of the expression.
func TestGoMirrorOfThePerRunComparison(t *testing.T) {
	m := func(v int64) *budget.Micros { x := budget.Micros(v); return &x }
	// mirror is perRunOK read as prose: the repository's own cap, else the
	// default, and the global one where it is set; with neither side set the
	// comparison is false (the rules deny).
	mirror := func(repo, def, global *budget.Micros, delta budget.Micros) bool {
		if repo != nil || def != nil {
			own := repo
			if own == nil {
				own = def
			}
			return delta <= *own && (global == nil || delta <= *global)
		}
		return global != nil && delta <= *global
	}
	for _, sides := range []struct {
		name          string
		repo, def, gl *budget.Micros
	}{
		{"none", nil, nil, nil},
		{"repo", m(30), nil, nil},
		{"default", nil, m(10), nil},
		{"global", nil, nil, m(20)},
		{"repo+default", m(30), m(10), nil},
		{"repo+global", m(30), nil, m(20)},
		{"default+global", nil, m(10), m(20)},
		{"repo+default+global", m(30), m(10), m(20)},
	} {
		t.Run(sides.name, func(t *testing.T) {
			c := budget.Caps{}
			if sides.repo != nil {
				c.Repo = &budget.RepoCaps{PerRunMicros: sides.repo}
			}
			if sides.def != nil {
				c.Defaults = &budget.DefaultCaps{RepoPerRunMicros: sides.def}
			}
			if sides.gl != nil {
				c.Global = &budget.GlobalCaps{PerRunMicros: sides.gl}
			}
			cap, ok := c.PerRun()
			for _, delta := range []budget.Micros{0, 5, 10, 20, 30, 50} {
				if got, want := mirror(sides.repo, sides.def, sides.gl, delta), ok && delta <= cap; got != want {
					t.Errorf("delta %d: the rules' branches say %v, Caps.PerRun %d %v says %v", delta, got, cap, ok, want)
				}
			}
		})
	}
}

// TestGoMirrorOfTheDayKeys: the expression the rules use for TODAY is the same
// arithmetic as budget.Day, checked with a Go mirror around the UTC midnight
// (the emulator has no settable clock, so the boundary cannot be exercised
// there; TestTodayExpression runs the live half).
func TestGoMirrorOfTheDayKeys(t *testing.T) {
	g, _ := Generate()
	if !strings.Contains(string(g), `('' + ((now - now % 86400000) / 86400000))`) {
		t.Fatal("the TODAY expression changed: update the mirror below with it")
	}
	mirror := func(nowMs float64) string { // '' + ((now - now % 86400000) / 86400000)
		r := nowMs - float64(int64(nowMs)%86400000)
		return trimInt(r / 86400000)
	}
	for _, at := range []time.Time{
		time.Date(2026, 10, 2, 23, 59, 59, 999_000_000, time.UTC),
		time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 10, 3, 0, 0, 0, 1_000_000, time.UTC),
		time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2030, 12, 31, 23, 59, 59, 999_000_000, time.UTC),
	} {
		if got, want := mirror(float64(at.UnixMilli())), budget.DayKey(budget.Day(at)); got != want {
			t.Errorf("%s: rules say %q, budget.Day says %q", at, got, want)
		}
	}
}

func trimInt(f float64) string {
	return strings.TrimSuffix(strings.TrimSuffix(formatFloat(f), ".0"), ".")
}

func formatFloat(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}
