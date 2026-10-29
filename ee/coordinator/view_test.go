// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

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
//
// The fixture's #18 is the control, and it is the reason the fixture carries
// it: its run changed internal/gitprov/github/apptoken.go, so #18 is in flight
// and shares no file with any other record — and it shares no *service* with
// them either, which is the half this file's own test would not have caught
// before ServiceOf took two segments under internal/: under the first-segment
// rule every record here was "internal" and #18 collided with all four.
//
// The pairs are also ORDERED, and the fixture is built so that the order says
// which one: the running run 20261013-140000-eeee has no pull request, so its
// record is labelled by its run ID and sortRecords lists it before every
// numbered one -- while its label sorts AFTER every `#n`. A view that sorted
// its collisions by label would put #12 and #14 first; the view in this
// package lists the pairs by their records' own order, so the run's two
// collisions come first. Both orders name the same three pairs, which is why
// this assertion is the one that can tell them apart.
func TestCoordinatorViewCollisionsNameBothRecords(t *testing.T) {
	v := loadView(t)
	var got []string
	for _, c := range v.Collisions {
		got = append(got, c.A+" and "+c.B)
	}
	want := []string{
		"20261013-140000-eeee and #12",
		"20261013-140000-eeee and #14",
		"#12 and #14",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("the collisions are %q, want %q: one pair per pair of in-flight records, in the order the records are listed", got, want)
	}
	c, ok := collisionOf(v, "#12", "#14")
	if !ok {
		t.Fatalf("the collision between #12 and #14 is missing: %+v", v.Collisions)
	}
	if want := []string{"internal/cli/exec.go", "internal/coordinator/view.go"}; !slices.Equal(c.Files, want) {
		t.Errorf("the collision's files = %q, want %q", c.Files, want)
	}
	// The shared *service* is each package the two records' paths sit in, not
	// the trunk they sit under: the pair shares internal/cli and
	// internal/coordinator, and a rule that answered "internal" here would
	// collide every pair of pull requests in this repository with every other.
	if want := []string{"internal/cli", "internal/coordinator"}; !slices.Equal(c.Services, want) {
		t.Errorf("the collision's services = %q, want %q", c.Services, want)
	}
	for _, c := range v.Collisions {
		if c.A == "#16" || c.B == "#16" {
			t.Errorf("the merged pull request #16 is in a collision: %+v", c)
		}
		// #18 is in flight and shares no file with any other record, so a
		// collision naming it can only have come from a service: under the
		// first-segment rule it collided with every record here, and this is
		// where that shows.
		if c.A == "#18" || c.B == "#18" {
			t.Errorf("the pull request #18, whose one changed path is in a package of its own, collides: %+v", c)
		}
	}
}

// collisionOf is the view's collision between the two labelled records, in
// either order, and whether the view holds one.
func collisionOf(v View, a, b string) (Collision, bool) {
	for _, c := range v.Collisions {
		if (c.A == a && c.B == b) || (c.A == b && c.B == a) {
			return c, true
		}
	}
	return Collision{}, false
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
		t.Fatalf("no golden file: run go test ./ee/coordinator -run TestCoordinatorViewGolden -update")
	}
	if string(got) != string(want) {
		t.Errorf("the view changed (go test ./ee/coordinator -update to accept):\ngot:\n%s\nwant:\n%s", got, want)
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
	// The service of a path is its first two segments under the three trees
	// that hold more than one unit, and its first segment everywhere else. The
	// cases that look odd are the rule's own edges: "internal" and "internals"
	// are not the tree, "internal/" and "internal/notes.go" have no second
	// *directory* and are answered from the path's shape rather than from a
	// tree the view never reads.
	for _, tc := range []struct{ path, want string }{
		{"internal/coordinator/view.go", "internal/coordinator"},
		{"internal/cli/exec.go", "internal/cli"},
		{"internal/notes.go", "internal/notes.go"},
		{"internal/", "internal"},
		{"internal", ""},
		{"internals/x.go", "internals"},
		{"cmd/fugaro/main.go", "cmd/fugaro"},
		{"cmd/", "cmd"},
		{"pkg/budget/store.go", "pkg/budget"},
		{"schemas/result.schema.json", "schemas"},
		{"website/content/cli-reference.md", "website"},
		{"docs/coordinator/README.md", "docs"},
		{"go.mod", ""},
		{"", ""},
		{"/absolute/path.go", ""},
	} {
		if got := ServiceOf(tc.path); got != tc.want {
			t.Errorf("ServiceOf(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// Two records in flight collide on a *service* as well as on a file, and the
// service is ServiceOf's answer. The failure this test is written against is
// the rule's earlier shape — the first segment alone — under which every path
// under internal/, which is most of this repository, collapsed into the one
// service "internal": two pull requests working in packages that share no file
// at all were reported as colliding, and the view's whole claim to say
// something useful about who is touching what came down to "somebody else is
// editing this repository".
//
// #2 and #3 are in internal/cli together and collide on it, with no file
// between them; #1 is the pair's control, and the assertion is that it collides
// with neither.
func TestCoordinatorViewDifferentPackagesDoNotCollide(t *testing.T) {
	v := Join("acme/app", nil, []Run{
		{ID: "20261014-100000-aaaa", PR: 1, Status: "running", Files: []string{"internal/gitprov/github/apptoken.go"}},
		{ID: "20261014-110000-bbbb", PR: 2, Status: "running", Files: []string{"internal/cli/exec.go"}},
		{ID: "20261014-120000-cccc", PR: 3, Status: "running", Files: []string{"internal/cli/runs.go"}},
	})
	var got []string
	for _, c := range v.Collisions {
		got = append(got, c.A+" and "+c.B)
	}
	if want := []string{"#2 and #3"}; !slices.Equal(got, want) {
		t.Fatalf("the collisions are %q, want %q: two packages under internal/ are two services, and two records collide only when they touch one", got, want)
	}
	if c := v.Collisions[0]; !slices.Equal(c.Files, []string{}) || !slices.Equal(c.Services, []string{"internal/cli"}) {
		t.Errorf("the collision of #2 and #3 = files %q, services %q; want no files and the one shared service internal/cli",
			c.Files, c.Services)
	}
}

// A record's runs are its runs newest first, and the view says so: a run ID
// starts with its mint time, so the newest is the one behind #9's `declared
// by` cell and the one the table's reader is shown first. The failure this
// guards is the store's order, which is not the view's: handed the runs
// oldest first, a view that kept them would name the oldest launcher and read
// as a record nobody has touched since.
func TestCoordinatorViewRunsAreNewestFirst(t *testing.T) {
	v := Join("acme/app", nil, []Run{
		{ID: "20261012-090000-0000", PR: 9, Status: "succeeded", Terminal: true, Files: []string{}},
		{ID: "20261014-090000-bbbb", PR: 9, Status: "running", Files: []string{}},
		{ID: "20261013-090000-aaaa", PR: 9, Status: "succeeded", Terminal: true, Files: []string{}},
	})
	r, ok := recordOf(v, "#9")
	if !ok {
		t.Fatalf("the view dropped #9: %+v", v.Records)
	}
	var ids []string
	for _, run := range r.Runs {
		ids = append(ids, run.ID)
	}
	want := []string{"20261014-090000-bbbb", "20261013-090000-aaaa", "20261012-090000-0000"}
	if !slices.Equal(ids, want) {
		t.Errorf("#9 holds runs %q, want %q: a record's runs are newest first, whatever order the reader returned them in", ids, want)
	}
}

// The release a record declares is its branch's, and its base branch's when
// the branch declares none: a pull request targeting release/v0.12.0 is work
// for that release even though the run's own branch — fugaro/… — never names
// one, and a record that reported no release would leave that work out of the
// release view the spec's §9 asks for. The branch wins when both declare one:
// the branch is what the pull request is.
func TestCoordinatorViewReleaseFromTheBaseBranch(t *testing.T) {
	for _, tc := range []struct {
		name       string
		branch     string
		baseBranch string
		want       string
	}{
		{"the base branch declares it", "fugaro/20261014-100000-aaaa", "release/v0.12.0", "0.12.0"},
		{"the branch wins over the base", "release/v0.11.0", "release/v0.12.0", "0.11.0"},
		{"neither declares one", "fugaro/20261014-100000-aaaa", "main", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := Join("acme/app", nil, []Run{{
				ID: "20261014-100000-aaaa", PR: 4, Status: "running",
				Branch: tc.branch, BaseBranch: tc.baseBranch, Files: []string{},
			}})
			r, ok := recordOf(v, "#4")
			if !ok {
				t.Fatalf("the view dropped #4: %+v", v.Records)
			}
			if r.Release != tc.want {
				t.Errorf("#4 declares release %q, want %q", r.Release, tc.want)
			}
		})
	}
}
