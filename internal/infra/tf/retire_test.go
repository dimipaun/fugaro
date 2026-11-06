package tf

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The pin retire.go exists for (issue #1794, requirement 3): a module that
// declares a resource type neither table decided fails here, so the retirement
// a managed project's plain update makes by itself can never reach a type
// nobody thought about — and the day a module grows a bucket-shaped resource,
// this test is what says so before a run does.
//
// The other direction is pinned too, and for the same reason: a table that
// decides a type no module declares is a decision about nothing, and one that
// decides a type twice has no decision at all.
func TestRetirableTypesDecideTheModules(t *testing.T) {
	root := filepath.Join("..", "..", "..", "deploy", "terraform")
	resRE := regexp.MustCompile(`(?m)^resource "([a-z0-9_]+)"`)
	used, n := map[string]bool{}, 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".tf") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		n++
		for _, m := range resRE.FindAllStringSubmatch(string(b), -1) {
			used[m[1]] = true
		}
		return nil
	})
	if err != nil || n < 20 {
		t.Fatalf("read %d module files: %v", n, err)
	}
	for typ := range used {
		if _, ok := RetirableTypes[typ]; ok {
			continue
		}
		if _, ok := RetainedTypes[typ]; ok {
			continue
		}
		t.Errorf("a module declares %s and neither RetirableTypes nor RetainedTypes decided it: a managed project's own retirement may not act on a type nobody decided", typ)
	}
	decided := func(what string, table map[string]string) {
		for typ := range table {
			if !used[typ] {
				t.Errorf("%s decides %s, which no module declares", what, typ)
			}
		}
	}
	decided("RetirableTypes", RetirableTypes)
	decided("RetainedTypes", RetainedTypes)
	for typ := range RetirableTypes {
		if _, both := RetainedTypes[typ]; both {
			t.Errorf("%s is in both tables: a type has exactly one decision", typ)
		}
	}
	// And the decision reads the way the retirement asks it: an unknown type
	// is refused, with a reason of its own rather than by silence.
	if retirable, why := RetirableType("google_unknown_thing"); retirable || why == "" {
		t.Errorf("RetirableType(an unknown type) = %v, %q; want a refusal with a reason", retirable, why)
	}
	if retirable, why := RetirableType("google_secret_manager_secret"); retirable || !strings.Contains(why, "value") {
		t.Errorf("RetirableType(a secret container) = %v, %q; want the refusal that names the value it holds", retirable, why)
	}
	if retirable, why := RetirableType("google_cloud_run_v2_job"); !retirable || why == "" {
		t.Errorf("RetirableType(a job) = %v, %q; want the derived side of the decision", retirable, why)
	}
}
