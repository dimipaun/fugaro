package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestLoadNewerVersionRefused pins D3422's refusal: a fugaro.yaml newer than
// the reader is refused in one line naming `brew upgrade`, and never read
// half-way. The file it refuses is a v1 file with one number changed, because
// the refusal is about the version key alone — nothing else in the file is
// looked at, which is what lets a release refuse a shape it has never seen.
//
// The three readers a launcher has before it resolves a whole config answer
// the same way: Parse, and the two best-effort reads (RepoOf, AgentOf) that
// say "cannot tell" rather than hand a caller half a document.
func TestLoadNewerVersionRefused(t *testing.T) {
	data := []byte(strings.Replace(minimalYAML, "version: 1", "version: 2", 1))
	c, ps := Parse(data)
	if c != nil {
		t.Fatal("a newer file parsed")
	}
	if len(ps) != 1 {
		t.Fatalf("want one problem, got %v", ps)
	}
	got := ps[0].String()
	if ps[0].Path != "version" {
		t.Errorf("problem path = %q, want version", ps[0].Path)
	}
	if !strings.Contains(got, "brew upgrade") {
		t.Errorf("the refusal does not name brew upgrade: %q", got)
	}
	if !strings.Contains(got, "2") {
		t.Errorf("the refusal does not name the file's own version: %q", got)
	}
	if s := ps[0].Message; strings.Contains(s, "\n") || len(s) > 140 {
		t.Errorf("the refusal is %d characters: %q", len(s), s)
	}
	if _, ok := RepoOf(data); ok {
		t.Error("RepoOf read a newer file")
	}
	if _, ok := AgentOf(data); ok {
		t.Error("AgentOf read a newer file")
	}
}

// TestUpgradersChain pins D3422's loader: the registered upgraders run in
// order, one version at a time, from the version the file was written at up to
// the target, and a step nothing is registered for is taken with no change. v1
// is the identity today, so the two upgraders here are registered by this test
// alone — a chain needs two of them to prove the order and the stop.
func TestUpgradersChain(t *testing.T) {
	var ran []int
	upgraders[1] = func(tree map[string]any) error {
		ran = append(ran, 1)
		tree["mark"] = "one"
		return nil
	}
	upgraders[2] = func(tree map[string]any) error {
		ran = append(ran, 2)
		if tree["mark"] != "one" {
			t.Errorf("the 1 → 2 step did not run first: mark = %v", tree["mark"])
		}
		tree["mark"] = "two"
		return nil
	}
	t.Cleanup(func() {
		delete(upgraders, 1)
		delete(upgraders, 2)
	})

	out, ps := upgradeTo([]byte(minimalYAML), 3)
	if len(ps) > 0 {
		t.Fatalf("upgrading a v1 file to 3: %v", ps)
	}
	if len(ran) != 2 || ran[0] != 1 || ran[1] != 2 {
		t.Errorf("the chain ran %v, want 1 then 2", ran)
	}
	var c Config
	if err := yaml.Unmarshal(out, &c); err != nil {
		t.Fatalf("the upgraded document does not decode: %v", err)
	}
	if c.Version != 3 {
		t.Errorf("the upgraded document is version %d, want 3", c.Version)
	}
	if c.Project != "aurora" || len(c.Workflows) != 1 {
		t.Errorf("the upgrade lost the rest of the file: %+v", c)
	}

	// A version with no upgrader registered is a step the loader takes without
	// changing anything, and the version key still lands on the target.
	delete(upgraders, 2)
	ran = nil
	out, ps = upgradeTo([]byte(minimalYAML), 3)
	if len(ps) > 0 {
		t.Fatalf("upgrading with a gap in the chain: %v", ps)
	}
	if len(ran) != 1 || ran[0] != 1 {
		t.Errorf("the chain ran %v, want only the registered step 1", ran)
	}
	if err := yaml.Unmarshal(out, &c); err != nil {
		t.Fatalf("the upgraded document does not decode: %v", err)
	}
	if c.Version != 3 {
		t.Errorf("a gap in the chain left the document at version %d, want 3", c.Version)
	}
}

// TestV1ReadsUnchanged pins that the versioned loader is inert today: v1 is the
// identity, so a file at CurrentVersion comes back byte for byte, no upgrader
// runs, and the config Parse has always handed back is the one it hands back
// now (D3422). It is the row that lets D3422 change the loader without changing
// a single file's meaning.
func TestV1ReadsUnchanged(t *testing.T) {
	data := []byte(minimalYAML)
	up, ps := upgradeVersion(data)
	if len(ps) > 0 {
		t.Fatalf("a v%d file does not load: %v", CurrentVersion, ps)
	}
	if string(up) != string(data) {
		t.Errorf("a v%d file is rewritten on load:\n%s", CurrentVersion, up)
	}
	c, ps := Parse(data)
	if len(ps) > 0 {
		t.Fatalf("a v%d file does not parse: %v", CurrentVersion, ps)
	}
	if c.Version != CurrentVersion || c.Project != "aurora" {
		t.Errorf("parsed version %d project %q, want %d aurora", c.Version, c.Project, CurrentVersion)
	}
	// The table and the loader's target are one fact: the newest supported
	// version is the one a file is lifted to.
	if last := SupportedVersions[len(SupportedVersions)-1].Number; last != CurrentVersion {
		t.Errorf("SupportedVersions ends at %d but the loader lifts to %d", last, CurrentVersion)
	}
}
