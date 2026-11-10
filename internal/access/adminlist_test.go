package access

// The derived admin list (D1940, F32): the bucket object `fugaro update`
// writes for the intake, and the two properties that make it the thing the
// intake may decide on — it holds the roster's project-scoped admins and
// nobody else, and it lives where no launcher credential reaches.

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

// TestAdminListHoldsOnlyProjectScopedAdmins is the plan's red step: a viewer in
// the roster appears in the list before the filter. The list is the intake's
// whole answer to "may this caller write a shared secret" (D1935, D1940), so
// every grant that is not an admin of the whole project is a person the intake
// must never find on it: a viewer and an operator hold no such write at all, an
// all-repos or repository-scoped admin holds it inside their repositories and
// not over the installation's shared prefix, and a grant whose role or scope
// this package did not parse grants nothing and is on no list.
func TestAdminListHoldsOnlyProjectScopedAdmins(t *testing.T) {
	grants := []Grant{
		{User: "user:owner@example.com", Role: RoleAdmin, Scope: Scope{Kind: ScopeProject}},
		{User: "user:deputy@example.com", Role: RoleAdmin, Scope: Scope{Kind: ScopeProject}},
		{User: "user:root@example.com", Role: RoleAdmin, Scope: Scope{Kind: ScopeAllRepos}},
		{User: "user:apps@example.com", Role: RoleAdmin, Scope: Scope{Kind: ScopeRepos, Repos: []string{"github/acme/app"}}},
		{User: "user:watcher@example.com", Role: RoleViewer, Scope: Scope{Kind: ScopeProject}},
		{User: "user:operator@example.com", Role: RoleOperator, Scope: Scope{Kind: ScopeProject}},
		// A scope nobody parsed and a role outside the closed set: both grant
		// nothing, and neither may reach the list by having the right word in
		// the other field.
		{User: "user:nowhere@example.com", Role: RoleAdmin},
		{User: "user:norole@example.com", Scope: Scope{Kind: ScopeProject}},
	}

	list := Admins(grants)
	got := make([]string, 0, len(list))
	for _, a := range list {
		got = append(got, a.Email)
	}
	want := []string{"deputy@example.com", "owner@example.com"}
	if !slices.Equal(got, want) {
		t.Errorf("the list holds %v, want %v: it is the roster's project-scoped admins and nobody else", got, want)
	}
	for _, who := range []string{"root@example.com", "apps@example.com", "watcher@example.com", "operator@example.com", "nowhere@example.com", "norole@example.com"} {
		if slices.Contains(got, who) {
			t.Errorf("%s is on the list: their grant does not make them an admin of the whole project, and the list is what admits a caller to the shared prefix", who)
		}
	}

	// The object the intake reads, and what it may not carry: the intake
	// decides on membership alone, so a role, a scope or the repositories of
	// one must not travel with it. A list that carried them would invite the
	// intake to weigh them, and a second copy of a grant is a second thing to
	// keep in step with the roster.
	raw, err := EncodeAdminList(list)
	if err != nil {
		t.Fatalf("EncodeAdminList: %v", err)
	}
	var doc struct {
		Admins []map[string]any `json:"admins"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the encoded list is not the document this file writes: %v\n%s", err, raw)
	}
	if len(doc.Admins) != len(want) {
		t.Fatalf("the encoded list holds %d entries, want %d:\n%s", len(doc.Admins), len(want), raw)
	}
	for _, entry := range doc.Admins {
		for field := range entry {
			if field != "email" && field != "sub" {
				t.Errorf("an entry of %s carries %q: an entry has the address (and the %s, where one is known) and nothing else", PathAdminList, field, "sub")
			}
		}
	}
	back, err := ParseAdminList(raw)
	if err != nil {
		t.Fatalf("ParseAdminList(EncodeAdminList(list)): %v\n%s", err, raw)
	}
	if !slices.Equal(back, list) {
		t.Errorf("the list does not survive its own round trip: %v, want %v", back, list)
	}

	// An empty roster is an empty list, not a missing field: the object is
	// written on every update, so what the intake reads is always this
	// document, and a project whose roster holds no project-scoped admin says
	// so with [].
	empty, err := EncodeAdminList(Admins(nil))
	if err != nil {
		t.Fatalf("EncodeAdminList(Admins(nil)): %v", err)
	}
	if got, err := ParseAdminList(empty); err != nil || len(got) != 0 {
		t.Errorf("a roster with no project-scoped admin encodes as %s, want a document that reads back as no entry: %v, %v", empty, got, err)
	}

	t.Run("the reader refuses what the writer does not write", func(t *testing.T) {
		// The intake holds no database role and no roster (D1940): this
		// document is the whole of what it can check, so it is read whole or
		// not at all, and a reader that dropped the entry it could not read
		// would be acting on a list it only partly understood (fail closed).
		for name, raw := range map[string]string{
			"an unknown field in an entry":     `{"admins":[{"email":"owner@example.com","role":"admin"}]}`,
			"an unknown field in the document": `{"admins":[],"version":1}`,
			"a missing list":                   `{}`,
			"a null list":                      `{"admins":null}`,
			"a member, not an address":         `{"admins":[{"email":"user:owner@example.com"}]}`,
			"an address that is not canonical": `{"admins":[{"email":"Owner@Example.com"}]}`,
			"an empty address":                 `{"admins":[{"email":""}]}`,
			"no address":                       `{"admins":[{"sub":"1234567890"}]}`,
			"a sub that is not an account ID":  `{"admins":[{"email":"owner@example.com","sub":"owner@example.com"}]}`,
			"a field spelled twice":            `{"admins":[{"email":"owner@example.com","email":"deputy@example.com"}]}`,
			"a second document":                `{"admins":[]}{"admins":[]}`,
		} {
			if _, err := ParseAdminList([]byte(raw)); err == nil {
				t.Errorf("%s is read as a list: %s", name, raw)
			}
		}
	})
}

// TestAdminsDerivesOnlyWhatItsWriterAccepts: what Admins returns is a document
// EncodeAdminList writes, whatever the roster holds.
//
// The two disagree about exactly one address. Member admits a colon in the
// local part — it asks for printable ASCII, one "@" and no empty side — while
// an entry refuses any colon, because an address carrying one is the IAM member
// spelling the intake cannot match against a claim. So a single grant holding
// such an address is a roster from which the derivation builds a list its own
// writer refuses, and a refused list is no object at all: the intake would go
// on reading the list an earlier update derived, however often the door ran.
// The person is left off the list instead, which is what every other grant that
// does not parse gets, and the rest of the roster still reaches the intake.
func TestAdminsDerivesOnlyWhatItsWriterAccepts(t *testing.T) {
	grants := []Grant{
		{User: "user:owner@example.com", Role: RoleAdmin, Scope: Scope{Kind: ScopeProject}},
		{User: "user:adm:in@example.com", Role: RoleAdmin, Scope: Scope{Kind: ScopeProject}},
		{User: "user:deputy@example.com", Role: RoleAdmin, Scope: Scope{Kind: ScopeProject}},
	}
	// The grant is one the roster itself accepts (canonicalMember folds it to
	// itself), so nothing before the derivation filters it out.
	if _, err := canonicalMember("user:adm:in@example.com"); err != nil {
		t.Fatalf("the fixture's grant is not one the roster reads: %v", err)
	}

	list := Admins(grants)
	got := make([]string, 0, len(list))
	for _, a := range list {
		got = append(got, a.Email)
	}
	want := []string{"deputy@example.com", "owner@example.com"}
	if !slices.Equal(got, want) {
		t.Errorf("the list holds %v, want %v: an address the list cannot carry is a person the list does not hold, never a document the writer refuses", got, want)
	}
	raw, err := EncodeAdminList(list)
	if err != nil {
		t.Fatalf("EncodeAdminList(Admins(grants)): %v: one grant's spelling is not a reason for the intake to keep the list of an earlier update", err)
	}
	if strings.Contains(string(raw), "adm:in") {
		t.Errorf("the encoded list holds the colon-bearing address:\n%s", raw)
	}
}

// TestAdminListPathNotLauncherWritable is F32's other half: the object lives
// outside every path a launcher can write, because the caller the list admits
// is the one the intake decides against, and a caller who can write the list
// admits themselves.
//
// The prefixes are read out of the conditions themselves — gcp's launcher
// condition and the two prefix lists the job and build accounts are granted —
// rather than copied here: a list of prefixes written out in this test would
// pass while the real conditions moved, which is exactly the failure it exists
// to catch.
func TestAdminListPathNotLauncherWritable(t *testing.T) {
	const bucket = "fugaro-runs-example-project"

	launcher := launcherPrefixesOf(t, gcp.LauncherBucketCondition(bucket))
	if len(launcher) < 3 {
		t.Fatalf("the launcher condition names %v: this test read %q and found fewer than the three prefixes it grants, so it is asserting nothing", launcher, gcp.LauncherBucketCondition(bucket))
	}
	for _, granted := range []struct {
		why      string
		prefixes []string
	}{
		{"the launchers' own objectUser condition", launcher},
		{"the areas a job account writes (gcp.JobBucketPrefixes)", gcp.JobBucketPrefixes},
		{"the area a build account writes (gcp.BuildBucketPrefixes)", gcp.BuildBucketPrefixes},
		{"the shared issue store (gcp.IssuesBucketPrefix)", []string{gcp.IssuesBucketPrefix}},
		{"the sealed budget tokens (gcp.TokenBucketPrefix)", []string{gcp.TokenBucketPrefix}},
	} {
		for _, prefix := range granted.prefixes {
			if prefix == "" {
				t.Fatalf("%s names an empty prefix, which prefixes every object", granted.why)
			}
			if PathAdminList == prefix || strings.HasPrefix(PathAdminList, prefix+"/") {
				t.Errorf("%s is %s, under the prefix %q that %s grants: an account that can write it admits itself to the shared prefix, so the list must live outside every path a launcher, a job or a build can write",
					PathAdminList, PathAdminList, prefix, granted.why)
			}
		}
	}
}

// launcherPrefixesOf reads the object prefixes out of a bucket condition: the
// `objects/<prefix>/` segment of every clause, which is how
// gcp.LauncherBucketCondition spells the runs, tokens and issues prefixes.
func launcherPrefixesOf(t *testing.T, condition string) []string {
	t.Helper()
	clause := regexp.MustCompile(`objects/([^/"]+)/`)
	var out []string
	for _, m := range clause.FindAllStringSubmatch(condition, -1) {
		if !slices.Contains(out, m[1]) {
			out = append(out, m[1])
		}
	}
	return out
}
