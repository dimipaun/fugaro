package rules

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
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

// TestGoMirrorOfThePerRunBranches: the intended branches of the per-run
// comparison, written out in Go and checked against budget.Caps.PerRun for
// every combination of the three sides and a range of amounts.
//
// It does not read the generated expression, so it does not prove the rules
// agree with the client: TestPerRunOK pins the expression's shape, and the
// emulator suite (TestRulesParity and the directed rows) proves the
// agreement. This one runs everywhere, without the emulator, and makes a
// change of intent visible.
func TestGoMirrorOfThePerRunBranches(t *testing.T) {
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

// ---- The policy nodes (docs/design/project-centric.md §5, phase 1 task 1.2).
// These assertions run without the emulator, on the generated JSON; the emulator
// suite (TestAllowedPaths, TestDenialTable, build tag firebase) proves the same
// shape against the real evaluator.

// slugForPath stands in for the template's $slug when a policy path is
// compared with the generated rules (the template is JSON and holds the paths
// as literals; the wildcard is the only difference). It, and policyPaths below,
// live here rather than in rules.go: nothing outside these tests builds a
// policy path, and production code that imported internal/projectpolicy only to
// spell paths for a test pulled the policy package into the rules package.
const slugForPath = "x"

// policyPaths returns the policy's node paths as the template spells them, in
// the Go the policy uses: the shared node, one repository's child (with
// slugForPath in place of $slug) and the admin-only node. A rename in
// internal/projectpolicy updates both or fails
// TestTemplatePolicyPathsAgreeWithProjectpolicy (phase 1 task 1.2 step 3).
func policyPaths() (shared, repo, admin string) {
	return projectpolicy.PathShared(), projectpolicy.PathRepo(slugForPath), projectpolicy.PathAdmin()
}

// TestPolicyPathsEscapeASlugLikeBudgetKey: a slug is escaped exactly as
// budget.Key escapes it, so config/policy/repos/$slug and the rules'
// auth.token.fs claim agree — and it is compared with budget.Key itself, not
// with a hand-written copy of its table, so the two cannot drift while the
// copy stays green. The random strings exercise the escapes the table does not
// name.
func TestPolicyPathsEscapeASlugLikeBudgetKey(t *testing.T) {
	for _, slug := range []string{
		"acme-app-0123456789abcdef", "acme-app-0123", "a.b", "a$b", "a#b[c]d", "a/b", "100%", "\x01", "\x7f",
	} {
		if got, want := projectpolicy.PathRepo(slug), "config/policy/repos/"+budget.Key(slug); got != want {
			t.Errorf("PathRepo(%q) = %q, want %q (budget.Key)", slug, got, want)
		}
	}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 200; i++ {
		b := make([]byte, rng.Intn(24))
		for j := range b {
			b[j] = byte(rng.Intn(256))
		}
		slug := string(b)
		if slug == "" {
			continue
		}
		if got, want := projectpolicy.PathRepo(slug), "config/policy/repos/"+budget.Key(slug); got != want {
			t.Fatalf("PathRepo(%q) = %q, want %q (budget.Key)", slug, got, want)
		}
	}
	if got, want := projectpolicy.PathRepo(slugForPath), "config/policy/repos/"+slugForPath; got != want {
		t.Errorf("PathRepo(%q) = %q, want %q: the model slug the other tests compare with is its own key", slugForPath, got, want)
	}
	if projectpolicy.PathShared() != "config/policy/shared" || projectpolicy.PathAdmin() != "config/admin" {
		t.Errorf("PathShared/PathAdmin = %q/%q", projectpolicy.PathShared(), projectpolicy.PathAdmin())
	}
}

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
	shared, repo, admin := policyPaths()
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
	shared, repo, admin := policyPaths()

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
// item 12). One row per location a run writes — the list below, checked to be
// exhaustive against the generated rules, so a later edit that adds a
// run-writable location has to add it here too — asserting the grant is still
// there: a config-block edit must not quietly take away a run's ability to
// record its own spend, its own outcome or its registry entry.
func TestRunTokenWritesSurviveThePolicyEdit(t *testing.T) {
	paths := generatedPaths(t)
	// Every location the template grants a run token a write on, as it renders
	// with $slug = slugForPath ($run and $day stay as the template spells them).
	want := []string{
		// runs/<slug>/<run>/ (the run's lifetime ledger)
		"runs/" + slugForPath + "/$run/reserved",
		"runs/" + slugForPath + "/$run/released",
		"runs/" + slugForPath + "/$run/spent",
		"runs/" + slugForPath + "/$run/notional",
		"runs/" + slugForPath + "/$run/overrun",
		"runs/" + slugForPath + "/$run/tokens",
		"runs/" + slugForPath + "/$run/exp",
		// spend/<day>/global/ (the counted, spent, notional and call counters)
		"spend/$day/global/counted",
		"spend/$day/global/spent",
		"spend/$day/global/notional",
		"spend/$day/global/calls",
		// spend/<day>/repos/<slug>/ (the same, per repository)
		"spend/$day/repos/" + slugForPath + "/counted",
		"spend/$day/repos/" + slugForPath + "/spent",
		"spend/$day/repos/" + slugForPath + "/notional",
		"spend/$day/repos/" + slugForPath + "/calls",
		// spend/<day>/repos/<slug>/byModel/<model>/ (the per-model counters)
		"spend/$day/repos/" + slugForPath + "/byModel/$model/micros",
		"spend/$day/repos/" + slugForPath + "/byModel/$model/in",
		"spend/$day/repos/" + slugForPath + "/byModel/$model/out",
		"spend/$day/repos/" + slugForPath + "/byModel/$model/cr",
		"spend/$day/repos/" + slugForPath + "/byModel/$model/cw",
		// spend/<day>/runs/<slug>/<run>/ (the run's share of the day)
		"spend/$day/runs/" + slugForPath + "/$run/reserved",
		"spend/$day/runs/" + slugForPath + "/$run/released",
		"spend/$day/runs/" + slugForPath + "/$run/spent",
		// agents/<slug>/<run> (the registry entry) and its outcome
		"agents/" + slugForPath + "/$run",
		"outcomes/$day/" + slugForPath + "/$run",
	}
	for _, p := range want {
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

	// The list is exhaustive: every location under the run-token trees whose
	// .write is a granted expression is one of the rows above. A later edit
	// that adds a run-writable location fails here until it is listed.
	inList := map[string]bool{}
	for _, p := range want {
		inList[p] = true
	}
	for p, attrs := range paths {
		if _, ok := attrs[".write"].(string); !ok {
			continue
		}
		root := p
		if i := strings.IndexByte(p, '/'); i >= 0 {
			root = p[:i]
		}
		switch root {
		case "runs", "spend", "agents", "outcomes":
			if !inList[p] {
				t.Errorf("/%s is writable by a run token but is not one of this test's rows: add it", p)
			}
		case "mailbox":
			// Not a run token's tree, and named here so that the sweep has no
			// root it skips: the mailbox's writes are exactly four, and a
			// fifth is a new grant to a phone or to a consumer (D113, D113b).
			if _, listed := mailboxWrites()[p]; !listed {
				t.Errorf("/%s is writable under the mailbox: a write there is one of the four mailboxWrites() lists, and this one is not", p)
			}
		}
	}
}

// mailboxWrites is the mailbox's grant list: each writable location with the
// fragments its predicate must carry. The phone's are MAILBOX (the user claim,
// and $mb naming the token's own uid), the consumer's are CONSUMER (mc naming
// the mailbox it serves), and all four require newData.exists(), because
// validation does not run on a delete and a delete of a message is therefore
// denied. The two message nodes and the ack also require !data.exists():
// a message, and its ack, are written once, so neither writer can rewrite
// history (the phone a sent message, the consumer an outbox message or its
// ack). The heartbeat is the one node that is overwritten by design: it is
// the consumer's latest liveness, not a record. TestMailboxWritesAreExactlyTheFour
// pins the list against the generated rules and
// TestRunTokenWritesSurviveThePolicyEdit's sweep fails on any mailbox write
// that is not in it.
func mailboxWrites() map[string][]string {
	phone := []string{"auth.token.mu != null", "auth.token.mu == auth.uid", "$mb == auth.uid", "newData.exists()"}
	consumer := []string{"auth.token.mc != null", "auth.token.mc == $mb", "newData.exists()"}
	once := func(f []string) []string { return append(slices.Clone(f), "!data.exists()") }
	return map[string][]string{
		"mailbox/$mb/inbox/$msg":        once(phone),
		"mailbox/$mb/outbox/$msg/acked": once(phone),
		"mailbox/$mb/outbox/$msg":       once(consumer),
		"mailbox/$mb/heartbeat":         consumer,
	}
}

// TestMailboxWritesAreExactlyTheFour pins the mailbox's grant list without an
// emulator: D113's two phone writes (its own inbox message, the ack on a
// message it spoke), D113b's two consumer writes (the outbox message, the
// heartbeat), and nothing else. The emulator proves what each identity can
// reach through them (emulator_mailbox_test.go, plan task P2.2); this proves
// the list did not grow, which is the half a later edit would change quietly.
func TestMailboxWritesAreExactlyTheFour(t *testing.T) {
	paths := generatedPaths(t)
	want := mailboxWrites()
	for p, frags := range want {
		attrs, ok := paths[p]
		if !ok {
			t.Errorf("the template does not generate /%s", p)
			continue
		}
		w, _ := attrs[".write"].(string)
		if w == "" {
			t.Errorf("/%s carries no .write: one of the mailbox's four writes is gone", p)
			continue
		}
		for _, f := range frags {
			if !strings.Contains(w, f) {
				t.Errorf("/%s .write = %q, want it to carry %q", p, w, f)
			}
		}
	}

	// No fifth: every writable location under the mailbox is one of the four.
	// TestRunTokenWritesSurviveThePolicyEdit's sweep fails on any of them
	// that is not listed here, so a new grant has to be argued for in both.
	for p, attrs := range paths {
		if p != "mailbox" && !strings.HasPrefix(p, "mailbox/") {
			continue
		}
		if w, ok := attrs[".write"].(string); ok {
			if _, listed := want[p]; !listed {
				t.Errorf("/%s is writable (%s) and is not one of the mailbox's four writes", p, w)
			}
		}
	}

	// The read side, which is the other half of the fence: the phone reads its
	// own outbox and heartbeat, and reads no inbox — its own included, since
	// the consumer collects what the phone wrote (§6.4).
	for _, p := range []string{"mailbox/$mb/outbox", "mailbox/$mb/heartbeat"} {
		attrs, ok := paths[p]
		if !ok {
			t.Errorf("the template does not generate /%s", p)
			continue
		}
		r, _ := attrs[".read"].(string)
		for _, f := range []string{"auth.token.mu == auth.uid", "$mb == auth.uid"} {
			if !strings.Contains(r, f) {
				t.Errorf("/%s .read = %q, want it to carry %q", p, r, f)
			}
		}
	}
	for _, p := range []string{"mailbox", "mailbox/$mb", "mailbox/$mb/inbox", "mailbox/$mb/inbox/$msg"} {
		attrs, ok := paths[p]
		if !ok {
			t.Errorf("the template does not generate /%s", p)
			continue
		}
		if r, ok := attrs[".read"]; ok && r != false {
			t.Errorf("/%s .read = %v: an RTDB grant on a parent cascades to its children", p, r)
		}
	}
}

// ---- The mailbox and the shared schema (mobile plan P2.1, design m9 §6.4).

// schemaPath is the shared schema, from this package's directory: the two
// artifacts are separate (the schema is the phone's, the rules are the
// database's) and nothing but a test can hold them together.
const schemaPath = "../../../shared/mailbox-schema/index.js"

// schemaCatalog reads shared/mailbox-schema/index.js and returns its exported
// constants by name, and the string literals of its two field lists. It is a
// deliberately small reader of a deliberately simple file: every export in it
// is `export const NAME = <number, Number.MAX_SAFE_INTEGER, or a frozen array
// of strings>;`, so a value this cannot parse is a failure rather than a
// silently unchecked number.
func schemaCatalog(t *testing.T) (consts map[string]int64, fields, required []string) {
	t.Helper()
	b, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("reading the shared schema: %v", err)
	}
	consts = map[string]int64{}
	for _, m := range regexp.MustCompile(`(?m)^export const ([A-Z_]+) = ([^;]+);`).FindAllStringSubmatch(string(b), -1) {
		name, expr := m[1], strings.TrimSpace(m[2])
		switch {
		case regexp.MustCompile(`^\d+$`).MatchString(expr):
			n, err := strconv.ParseInt(expr, 10, 64)
			if err != nil {
				t.Fatalf("%s = %s: %v", name, expr, err)
			}
			consts[name] = n
		case expr == "Number.MAX_SAFE_INTEGER":
			consts[name] = 1<<53 - 1
		case strings.HasPrefix(expr, "Object.freeze({"):
			// A frozen map of names (REFUSALS): no bound in it to compare.
		case strings.HasPrefix(expr, "Object.freeze(["):
			consts[name] = 0
			var got []string
			for _, s := range regexp.MustCompile(`'([^']*)'`).FindAllStringSubmatch(expr, -1) {
				got = append(got, s[1])
			}
			switch name {
			case "FIELDS":
				fields = got
			case "REQUIRED_FIELDS":
				required = got
			}
		default:
			t.Fatalf("shared/mailbox-schema exports %s = %s, which this reader does not understand: teach it, so the number stays checked", name, expr)
		}
	}
	if len(consts) == 0 || len(fields) == 0 || len(required) == 0 {
		t.Fatalf("shared/mailbox-schema: read %d constants, %d fields and %d required fields", len(consts), len(fields), len(required))
	}
	return consts, fields, required
}

// TestMailboxRulesAgreeWithTheSharedSchema pins, without the emulator, the part
// of §6.4's "the two artifacts cannot disagree" that can be pinned in Go: the
// four bounds the rules apply to a message are the schema's own numbers, and
// the fields the rules accept are the schema's field set. Both directions
// matter, and the direction that matters more is the one that would refuse a
// message the phone accepted: a bound the rules apply tighter than the schema
// (at held to MaxAmount, an id to something the schema allows longer) fails
// here.
//
// P6.3 grew the schema — `mac` is now required on every message, and `agent`,
// `kind`, `options`, `reply_to` and the action record are optional fields — and
// the template has NOT grown with it: the mailbox subtree still gates P2.1's
// field set, and P6.4 (a task of its own, level 3, after P2.2) is what adds the
// new fields to the rules. So the two lists below are pinned in the direction
// that must hold NOW: the schema's field set is exactly this (a change to it
// is a deliberate change to this test), and every field the rules DO gate is a
// field the schema names, never one it does not. The fields the rules do not
// gate yet are named in the comment above the per-node loop, so that "fewer
// fields than the schema" is a known, listed state rather than an oversight —
// and so that P6.4 has a failing test to make pass, not a silent gap to close.
//
// What this test cannot pin is the rest of the schema's judgement, and §6.4
// says where the rules are looser rather than claiming they are the same: the
// rules cannot inspect characters, so the id's key rule (no `%`, no control
// character, no lone surrogate — all of which the schema refuses) is the
// schema's alone, and `text`'s control characters with it. The emulator cases
// (emulator_mailbox_test.go) prove what the rules do refuse.
func TestMailboxRulesAgreeWithTheSharedSchema(t *testing.T) {
	consts, fields, required := schemaCatalog(t)
	if got, want := MaxKeyLength, consts["ID_MAX"]; int64(got) != want {
		t.Errorf("MaxKeyLength = %d, the schema's ID_MAX = %d", got, want)
	}
	if got, want := MaxString, consts["SENDER_MAX"]; int64(got) != want {
		t.Errorf("MaxString = %d, the schema's SENDER_MAX = %d", got, want)
	}
	if got, want := MaxText, consts["TEXT_MAX"]; int64(got) != want {
		t.Errorf("MaxText = %d, the schema's TEXT_MAX = %d", got, want)
	}
	if got, want := MaxTimestamp, consts["AT_MAX"]; got != want {
		t.Errorf("MaxTimestamp = %d, the schema's AT_MAX = %d", got, want)
	}
	if want := []string{"id", "sender", "text", "at", "mac"}; !slices.Equal(required, want) {
		t.Errorf("the schema's REQUIRED_FIELDS = %v, want %v", required, want)
	}
	if want := []string{"id", "sender", "text", "at", "mac", "acked", "agent", "kind", "options", "reply_to", "action"}; !slices.Equal(fields, want) {
		t.Errorf("the schema's FIELDS = %v, want %v", fields, want)
	}

	// The generated mailbox applies those numbers, in the fields the schema
	// names. The heartbeat's `at` is the fourth, and its `boot` is a sender-like
	// string: the schema does not model the heartbeat, so only the bound is
	// shared.
	paths := generatedPaths(t)
	for _, c := range []struct {
		path string
		frag string
	}{
		{`mailbox/$mb/inbox/$msg`, `$msg.length <= ` + strconv.Itoa(MaxKeyLength)},
		{`mailbox/$mb/inbox/$msg/id`, `newData.val().length <= ` + strconv.Itoa(MaxKeyLength)},
		{`mailbox/$mb/inbox/$msg/sender`, `newData.val().length <= ` + strconv.Itoa(MaxString)},
		{`mailbox/$mb/inbox/$msg/text`, `newData.val().length <= ` + strconv.Itoa(MaxText)},
		{`mailbox/$mb/inbox/$msg/at`, `newData.val() <= ` + strconv.FormatInt(MaxTimestamp, 10)},
		{`mailbox/$mb/outbox/$msg`, `$msg.length <= ` + strconv.Itoa(MaxKeyLength)},
		{`mailbox/$mb/outbox/$msg/text`, `newData.val().length <= ` + strconv.Itoa(MaxText)},
		{`mailbox/$mb/outbox/$msg/at`, `newData.val() <= ` + strconv.FormatInt(MaxTimestamp, 10)},
		{`mailbox/$mb/heartbeat/at`, `newData.val() <= ` + strconv.FormatInt(MaxTimestamp, 10)},
	} {
		attrs, ok := paths[c.path]
		if !ok {
			t.Errorf("the template does not generate /%s", c.path)
			continue
		}
		v, _ := attrs[".validate"].(string)
		if !strings.Contains(v, c.frag) {
			t.Errorf("/%s .validate = %q, want it to carry the schema's own bound (%q)", c.path, v, c.frag)
		}
	}

	// The fields, per node: exactly what P2.1's template gates today, and every
	// one of them a field the schema names. An inbox message has the four
	// required P0.2 fields — `acked` is the phone's ack of a message it spoke,
	// which lives on an outbox message — and an outbox message has all five.
	//
	// P6.3's `mac` is NOT among them yet, and that is the gap P6.4 closes: the
	// schema now requires a tag on every message and the rules would refuse the
	// write that carries one. It is listed here, as a want that deliberately
	// lacks it, so the gap is visible in the test rather than only in a plan;
	// P6.4 adds "mac" to both lists and the same loop then asserts it. Until
	// then the second assertion below is what keeps the difference one-way: the
	// rules may gate FEWER fields than the schema, never a field it does not
	// name.
	for _, c := range []struct {
		path string
		want []string
	}{
		{`mailbox/$mb/inbox/$msg`, []string{"id", "sender", "text", "at"}},
		{`mailbox/$mb/outbox/$msg`, []string{"id", "sender", "text", "at", "acked"}},
	} {
		if _, ok := paths[c.path]; !ok {
			t.Errorf("the template does not generate /%s", c.path)
			continue
		}
		var got []string
		for p := range paths {
			name, ok := strings.CutPrefix(p, c.path+"/")
			if !ok || name == "$other" || strings.Contains(name, "/") {
				continue
			}
			got = append(got, name)
		}
		slices.Sort(got)
		want := slices.Clone(c.want)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("/%s holds %v, P2.1's field set for it is %v", c.path, got, want)
		}
		for _, field := range got {
			if !slices.Contains(fields, field) {
				t.Errorf("/%s gates %q, which the shared schema does not name: the rules may gate fewer fields than the schema, never one it does not have", c.path, field)
			}
		}
	}
}

// TestUserReadGrantsAreExactlyTheNamedFamilies: the read grants D112's answer
// adds, and nothing else. The user claim reads the eight expressions a run
// token already carried (each now `… || USER`) and the two nodes of its own
// mailbox — and a grant anywhere else, on a family tension (b1) struck or on a
// parent that would cascade to one, fails here as well as in the golden.
//
// F11's property is the half this pins from the other side: the eight are the
// only families a signed-in user reaches, so an account with no mu claim reads
// nothing (admitted nowhere), and no family of the run's own slices — agents
// and outcomes above all — was widened by this change.
func TestUserReadGrantsAreExactlyTheNamedFamilies(t *testing.T) {
	paths := generatedPaths(t)
	// The eight, and the two mailbox nodes, which are scoped to the mailbox
	// the claim owns.
	// generatedPaths spells $slug as slugForPath (it is the one wildcard the
	// tests substitute), so the keys below do too.
	want := map[string]bool{
		"config/caps/global":                       true,
		"config/caps/defaults":                     true,
		"config/caps/repos/" + slugForPath:         true,
		"config/kill/global":                       true,
		"config/kill/repos/" + slugForPath:         true,
		"spend/$day/global":                        true,
		"spend/$day/repos/" + slugForPath:          true,
		"spend/$day/runs/" + slugForPath + "/$run": true,
		"mailbox/$mb/outbox":                       true,
		"mailbox/$mb/heartbeat":                    true,
	}
	got := map[string]string{}
	for p, attrs := range paths {
		if r, ok := attrs[".read"].(string); ok && strings.Contains(r, "auth.token.mu") {
			got[p] = r
		}
	}
	for p := range want {
		r, ok := got[p]
		if !ok {
			t.Errorf("/%s does not carry the user claim's read grant: the app cannot read a family the owner granted it", p)
			continue
		}
		if strings.HasPrefix(p, "mailbox/") {
			if !strings.Contains(r, "$mb == auth.uid") {
				t.Errorf("/%s .read = %q, want the mailbox-scoped predicate ($mb == auth.uid)", p, r)
			}
			continue
		}
		if strings.Contains(r, "$mb") {
			t.Errorf("/%s .read = %q carries $mb: the dashboard families are the whole family, not one mailbox's", p, r)
		}
	}
	for p, r := range got {
		if !want[p] {
			t.Errorf("/%s carries the user claim's read grant (%s) and is not one of the families tension (b1) names", p, r)
		}
	}
}
