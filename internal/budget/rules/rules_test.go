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

// ---- The policy nodes (docs/design/project-centric.md §5, phase 1 task 1.2).
// These assertions run without the emulator, on the generated JSON; the emulator
// suite (TestAllowedPaths, TestDenialTable, build tag firebase) proves the same
// shape against the real evaluator.

// generatedPaths renders every location of the generated rules as a
// slash-separated path (attributes left out), with the template's $slug
// replaced by slugForPath, so a path built by internal/projectpolicy can be
// compared with what the template generated.
func generatedPaths(t *testing.T) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	walk(rulesTree(t), nil, func(path []string, attrs map[string]any) {
		if len(path) == 0 {
			return
		}
		segs := make([]string, len(path))
		for i, s := range path {
			if s == "$slug" {
				s = slugForPath
			}
			segs[i] = s
		}
		out[strings.Join(segs, "/")] = attrs
	})
	return out
}

// TestTemplatePolicyPathsAgreeWithProjectpolicy: the template holds the policy
// paths as JSON literals and internal/projectpolicy builds them in Go. The
// two cannot drift: a rename updates both or fails here (phase 1 task 1.2
// step 3).
func TestTemplatePolicyPathsAgreeWithProjectpolicy(t *testing.T) {
	shared, repo, admin := PolicyPaths()
	if shared != "config/policy/shared" || repo != "config/policy/repos/"+slugForPath || admin != "config/admin" {
		t.Fatalf("PolicyPaths = (%q, %q, %q), want config/policy/shared, config/policy/repos/x, config/admin", shared, repo, admin)
	}
	paths := generatedPaths(t)
	for _, p := range []string{shared, repo, admin} {
		if _, ok := paths[p]; !ok {
			t.Errorf("the template does not generate /%s, which internal/projectpolicy names", p)
		}
	}
}

// TestPolicyNodesAreReadPerChild pins §5(2) without the emulator: a TOKEN read
// on config/policy itself would cascade to every child, so each child carries
// its own grant and the parent carries none. config/admin is denied to a
// client entirely.
func TestPolicyNodesAreReadPerChild(t *testing.T) {
	paths := generatedPaths(t)
	shared, repo, admin := PolicyPaths()

	if attrs, ok := paths["config/policy"]; !ok {
		t.Fatal("the template does not generate /config/policy")
	} else {
		for _, a := range []string{".read", ".write"} {
			if v, ok := attrs[a]; ok {
				t.Errorf("/config/policy has %s = %v: an RTDB read grant on the parent cascades to every child", a, v)
			}
		}
	}
	if attrs, ok := paths["config/policy/repos"]; ok {
		for _, a := range []string{".read", ".write"} {
			if v, ok := attrs[a]; ok {
				t.Errorf("/config/policy/repos has %s = %v: the grant must be per $slug child", a, v)
			}
		}
	} else {
		t.Error("the template does not generate /config/policy/repos")
	}

	// The shared node: any token of the project, and read-only.
	attrs := paths[shared]
	read, _ := attrs[".read"].(string)
	if read == "" {
		t.Errorf("/%s has no .read: every run token must read the shared policy", shared)
	} else {
		if !strings.Contains(read, "auth.token.fp") {
			t.Errorf("/%s .read = %q, want the TOKEN claim check", shared, read)
		}
		if strings.Contains(read, "auth.token.fs") {
			t.Errorf("/%s .read = %q: the shared node is not slug-bound", shared, read)
		}
	}
	if w, ok := attrs[".write"]; !ok || w != false {
		t.Errorf("/%s .write = %v, want false: only IAM writes config/*", shared, w)
	}

	// A repository's node: the token's own slug only, and read-only.
	attrs = paths[repo]
	read, _ = attrs[".read"].(string)
	if !strings.Contains(read, "auth.token.fs == $slug") {
		t.Errorf("/%s .read = %q, want the auth.token.fs == $slug claim (as config/caps/repos/$slug has)", repo, read)
	}
	if w, ok := attrs[".write"]; !ok || w != false {
		t.Errorf("/%s .write = %v, want false", repo, w)
	}

	// The admin node: denied outright, in the rules. The rules are not what
	// keeps it from a launcher (IAM reads bypass them, §12a item 7), so the
	// assertion here is only that no client token gets in.
	attrs = paths[admin]
	if r, ok := attrs[".read"]; !ok || r != false {
		t.Errorf("/%s .read = %v, want false", admin, r)
	}
	if w, ok := attrs[".write"]; !ok || w != false {
		t.Errorf("/%s .write = %v, want false", admin, w)
	}

	// Nothing anywhere under config is writable by a client token, policy
	// included: admin writes are IAM-only.
	for p, a := range paths {
		if p != "config" && !strings.HasPrefix(p, "config/") {
			continue
		}
		if w, ok := a[".write"]; ok && w != false {
			t.Errorf("/%s .write = %v, want false under config", p, w)
		}
	}
}

// TestRunTokenWritesSurviveThePolicyEdit: the §5 edit is additive and confined
// to the config block, and a run token is not only a reader (design §4.3, §12a
// item 12). One row per location a run writes, asserting the grant is still
// there, so a later edit to config cannot quietly take away a run's ability to
// record its own spend, its own outcome or its registry entry.
func TestRunTokenWritesSurviveThePolicyEdit(t *testing.T) {
	paths := generatedPaths(t)
	for _, p := range []string{
		"runs/" + slugForPath + "/$run/reserved",
		"runs/" + slugForPath + "/$run/released",
		"runs/" + slugForPath + "/$run/spent",
		"runs/" + slugForPath + "/$run/notional",
		"runs/" + slugForPath + "/$run/overrun",
		"runs/" + slugForPath + "/$run/tokens",
		"runs/" + slugForPath + "/$run/exp",
		"spend/$day/global/counted",
		"spend/$day/global/spent",
		"spend/$day/global/notional",
		"spend/$day/global/calls",
		"spend/$day/repos/" + slugForPath + "/counted",
		"spend/$day/repos/" + slugForPath + "/spent",
		"spend/$day/repos/" + slugForPath + "/notional",
		"spend/$day/repos/" + slugForPath + "/calls",
		"agents/" + slugForPath + "/$run",
		"outcomes/$day/" + slugForPath + "/$run",
	} {
		attrs, ok := paths[p]
		if !ok {
			t.Errorf("/%s: the rules no longer generate this location", p)
			continue
		}
		w, ok := attrs[".write"].(string)
		if !ok || !strings.Contains(w, "auth.token") {
			t.Errorf("/%s .write = %v: a run token's write is not granted any more", p, attrs[".write"])
		}
	}
}
