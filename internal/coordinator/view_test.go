package coordinator

import (
	"encoding/json"
	"flag"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// updateGolden rewrites testdata/view.golden.json from the fixture, the way
// the CLI package's own goldens do.
var updateGolden = flag.Bool("update", false, "rewrite testdata/view.golden.json")

// viewFixture is testdata/view.json: one repository's pull requests and the
// runs the store holds for it, which is exactly what Join is handed. The
// fixture is read rather than built in Go so that the view a reader sees here
// is the view the file describes.
type viewFixture struct {
	Repo         string        `json:"repo"`
	PullRequests []PullRequest `json:"pull_requests"`
	Runs         []Run         `json:"runs"`
}

func loadView(t *testing.T) View {
	t.Helper()
	data, err := os.ReadFile("testdata/view.json")
	if err != nil {
		t.Fatal(err)
	}
	var f viewFixture
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("testdata/view.json: %v", err)
	}
	if f.Repo == "" || len(f.PullRequests) == 0 || len(f.Runs) == 0 {
		t.Fatal("testdata/view.json is not a fixture: it needs a repo, pull requests and runs")
	}
	return Join(f.Repo, f.PullRequests, f.Runs)
}

// recordOf is the view's record labelled label, and whether the view holds
// one: a record the view dropped is the failure mode these tests are written
// against, so absence is a value and not an index panic.
func recordOf(v View, label string) (Record, bool) {
	for _, r := range v.Records {
		if r.Label() == label {
			return r, true
		}
	}
	return Record{}, false
}

// The collision rule is one collision per *pair* of in-flight records, naming
// both, and the pair that shares a file shares it once however many runs
// touched it. The fixture's #12 and #14 both changed
// internal/coordinator/view.go; #16 changed it too and is merged, so it
// collides with neither.
func TestCoordinatorViewCollisionsNameBothRecords(t *testing.T) {
	v := loadView(t)
	if len(v.Collisions) != 1 {
		t.Fatalf("collisions = %d, want exactly 1: %+v", len(v.Collisions), v.Collisions)
	}
	c := v.Collisions[0]
	if c.A != "#12" || c.B != "#14" {
		t.Errorf("the collision names %s and %s, want #12 and #14", c.A, c.B)
	}
	if want := []string{"internal/cli/exec.go", "internal/coordinator/view.go"}; !slices.Equal(c.Files, want) {
		t.Errorf("the collision's files = %q, want %q", c.Files, want)
	}
	if want := []string{"internal"}; !slices.Equal(c.Services, want) {
		t.Errorf("the collision's services = %q, want %q", c.Services, want)
	}
	for _, c := range v.Collisions {
		if c.A == "#16" || c.B == "#16" {
			t.Errorf("the merged pull request #16 is in a collision: %+v", c)
		}
	}
}

// A pull request no run behind it is listed as such. It is work somebody has
// open, and the view's whole job is to say what is in flight: dropping it
// because the store has nothing to say about it is the failure this test
// exists for.
func TestCoordinatorViewListsPullRequestWithNoRun(t *testing.T) {
	v := loadView(t)
	r, ok := recordOf(v, "#17")
	if !ok {
		t.Fatalf("the view dropped #17, a pull request no run names: %+v", v.Records)
	}
	if len(r.Runs) != 0 {
		t.Errorf("#17 has runs %+v, want none", r.Runs)
	}
	if !r.InFlight {
		t.Error("#17 is an open pull request and the view calls it not in flight")
	}
	if r.Branch != "topic/hand-written" {
		t.Errorf("#17's branch = %q, want the host's own answer", r.Branch)
	}
}

// The launcher a run records is a declaration and is reported as one. Finding
// #11 is the reason: it is a string a launcher writes about itself, nothing
// verifies it, and the view must never present it as who did the work.
func TestCoordinatorViewDeclaredByIsADeclaration(t *testing.T) {
	v := loadView(t)
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, `"declared_by":"someone@example.com"`) {
		t.Errorf("the view does not carry the declaration under declared_by:\n%s", got)
	}
	for _, refused := range []string{
		`"launched_by"`, `"requested_by"`, `"author"`, `"user"`, `"actor"`, `"identity"`, `"owner"`,
	} {
		if strings.Contains(got, refused) {
			t.Errorf("the view carries %s: a self-declared string must be reported as a declaration, never as an identity", refused)
		}
	}
	// The same rule as a guard on the types, so that a field added later
	// under an identity-shaped name fails here rather than shipping.
	for _, name := range jsonNames(reflect.TypeOf(View{}), map[reflect.Type]bool{}) {
		if slices.Contains([]string{"launched_by", "requested_by", "author", "user", "actor", "identity", "owner"}, name) {
			t.Errorf("the view's types declare the field %q: an identity-shaped name for a self-declared string", name)
		}
	}
	if !slices.Contains(jsonNames(reflect.TypeOf(View{}), map[reflect.Type]bool{}), "declared_by") {
		t.Error("no field of the view is a declaration: the launcher has no home in the view")
	}
}

// jsonNames is every JSON object key the view's own types can emit, walked
// through their fields rather than listed by hand: a field added later is
// covered by the guard above without anyone remembering to add it.
func jsonNames(t reflect.Type, seen map[reflect.Type]bool) []string {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || seen[t] {
		return nil
	}
	seen[t] = true
	var out []string
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name != "" {
			out = append(out, name)
		}
		out = append(out, jsonNames(f.Type, seen)...)
	}
	return out
}

// A run still going that has opened no pull request is a record of its own —
// "who is working on what" before any code exists — and a run that has
// finished without one is not: finished work with no pull request is not work
// in flight.
func TestCoordinatorViewInFlightRunWithoutPullRequest(t *testing.T) {
	v := loadView(t)
	if _, ok := recordOf(v, "20261013-140000-eeee"); !ok {
		t.Errorf("the view dropped the running run 20261013-140000-eeee: %+v", v.Records)
	}
	if _, ok := recordOf(v, "20261009-090000-ffff"); ok {
		t.Errorf("the view lists 20261009-090000-ffff, a run that finished with no pull request: %+v", v.Records)
	}
}

// A run naming a pull request the reader did not return still names one: the
// record exists, with the host's own answer missing rather than invented, so
// that a reader which returned fewer pull requests than the store knows about
// loses no work.
func TestCoordinatorViewKeepsARunOnAnUnreadPullRequest(t *testing.T) {
	v := Join("acme/app", nil, []Run{{ID: "20261013-150000-1111", PR: 9, Status: "running", Files: []string{}}})
	if len(v.Records) != 1 {
		t.Fatalf("records = %+v, want the one run's", v.Records)
	}
	r := v.Records[0]
	if r.PR == nil || r.PR.Number != 9 {
		t.Fatalf("the record's pull request = %+v, want #9", r.PR)
	}
	if r.PR.State != "" {
		t.Errorf("the record invents the host's answer %q for a pull request the reader did not return", r.PR.State)
	}
	if !r.InFlight {
		t.Error("a running run on an unread pull request is not in flight")
	}
	if r.Label() != "#9" {
		t.Errorf("the record labels itself %q, want #9", r.Label())
	}
}

// The view is the fixture's, byte for byte, and it is read rather than
// accepted: every field of it is what a reader of this package sees.
func TestCoordinatorViewGolden(t *testing.T) {
	v := loadView(t)
	got, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	const path = "testdata/view.golden.json"
	if *updateGolden {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no golden file: run go test ./internal/coordinator -run TestCoordinatorViewGolden -update")
	}
	if string(got) != string(want) {
		t.Errorf("the view changed (go test ./internal/coordinator -update to accept):\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// The release a branch declares, and the service a path belongs to, are rules
// a reader has to be able to predict without running the code.
func TestCoordinatorReleaseOfAndServiceOf(t *testing.T) {
	for _, tc := range []struct{ branch, want string }{
		{"release/v0.11.0", "0.11.0"},
		{"release/v0.11", "0.11"},
		{"release/", ""},
		{"release/v0.11/rc1", ""},
		{"main", ""},
		{"fugaro/20261013-140000-eeee", ""},
		{"", ""},
	} {
		if got := ReleaseOf(tc.branch); got != tc.want {
			t.Errorf("ReleaseOf(%q) = %q, want %q", tc.branch, got, tc.want)
		}
	}
	for _, tc := range []struct{ path, want string }{
		{"internal/coordinator/view.go", "internal"},
		{"console/src/main/ipc.ts", "console"},
		{"go.mod", ""},
		{"", ""},
		{"/absolute/path.go", ""},
	} {
		if got := ServiceOf(tc.path); got != tc.want {
			t.Errorf("ServiceOf(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}
