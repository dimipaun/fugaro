package cli

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// project_cmd_test.go holds the tests of the two things this release adds to
// `fugaro project policy publish` itself: the refusal of a coding agent's
// session (SD8: the node this command writes is what every run of the project
// is admitted by, so it is a person's write and not an agent's) and the flags
// that are this door's own — --delete and --seed-repos — which the shared build
// the door now takes (policyNodeForPublish) must not swallow. Everything else
// about the command is project_policy_test.go's.
//
// It also holds the image catalog's node half (PIC2): the key the operator's
// catalog travels in, the member the publisher omits when there is no catalog,
// the refusal an older CLI makes of a node that carries the key, and the gate
// that is this catalog's and not the price catalog's.

// TestProjectPolicyPublishIsRefusedInAnAgentSession: every form of the door is
// refused before anything is read, --delete included — taking the policy away
// changes what every run is admitted by exactly as much as publishing one does,
// and it is not an agent's to do on its own. The refusal is a user error and
// not a crash, and it names the write it stopped.
func TestProjectPolicyPublishIsRefusedInAnAgentSession(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"publish", []string{"project", "policy", "publish"}},
		{"--seed-repos", []string{"project", "policy", "publish", "--seed-repos"}},
		{"--delete", []string{"project", "policy", "publish", "--delete", "--yes"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newProjectFixture(t, "")
			f.db.Set(projectpolicy.PathShared(), staleBudgetNode())
			t.Setenv("CLAUDECODE", "1")
			before := nodeOf(t, f.db)

			out, errOut, err := executeStdin(t, "", tc.args...)
			if err == nil {
				t.Fatalf("a coding agent's session wrote the policy:\n%s%s", out, errOut)
			}
			if got := ExitCode(err); got != ExitUserError {
				t.Errorf("exit %d, want %d", got, ExitUserError)
			}
			for _, want := range []string{"nothing was written", "coding agent", "fugaro project policy publish"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not say %q: %v", want, err)
				}
			}
			if after := nodeOf(t, f.db); after != before {
				t.Errorf("the database changed in a coding agent's session:\nbefore %s\nafter  %s", before, after)
			}
			if gen := recordedGeneration(t, f); gen != "" {
				t.Errorf("a coding agent's session recorded policy_generation = %q", gen)
			}
		})
	}
}

// TestProjectPolicyPublishKeepsDeleteAndSeedRepos: the two flags the door owns
// and the stage does not have. The node's allow-list is the node's own and a
// publish keeps it (P4), so the seed is only ever written when the operator asks
// for it by name — and a publish that went through the shared build and dropped
// the flag would look exactly like one that had never been asked.
func TestProjectPolicyPublishKeepsDeleteAndSeedRepos(t *testing.T) {
	t.Run("--seed-repos", func(t *testing.T) {
		f := newProjectFixture(t, "")
		node := staleBudgetNode()
		node["allow_repos"] = []string{"github/acme/hand-added"}
		f.db.Set(projectpolicy.PathShared(), node)

		if _, _, err := execute(t, "project", "policy", "publish", "--seed-repos"); err != nil {
			t.Fatal(err)
		}
		got := publishedPolicy(t, f).AllowRepos
		want := []string{"bitbucket/acme/other", "github/acme/app"}
		if len(got) != len(want) {
			t.Fatalf("allow_repos = %v, want the operator's repositories %v", got, want)
		}
		for i, r := range want {
			if got[i] != r {
				t.Fatalf("allow_repos = %v, want %v", got, want)
			}
		}
	})

	t.Run("--delete", func(t *testing.T) {
		f := newProjectFixture(t, "")
		f.db.Set(projectpolicy.PathShared(), staleBudgetNode())

		out, _, err := execute(t, "project", "policy", "publish", "--delete", "--yes")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if v := f.db.Value(projectpolicy.PathShared()); v != nil {
			t.Errorf("--delete left the node: %v", v)
		}
	})
}

// catalogConfigYAML is the operator's image catalog, written in the local
// configuration the way the spec's example writes it: one universal entry
// marked default and one narrow entry, so a test can tell the entries apart by
// name and see the marker survive the trip to the node.
const catalogConfigYAML = `image_catalog:
  entries:
    - name: universal
      tags: [node]
      default: true
      source: { kind: image, image: ghcr.io/acme/agent:1.2 }
    - name: rust
      tags: [cargo]
      source: { kind: dockerfile, repo: acme/other, path: docker/agent.Dockerfile }
`

// TestPolicyNodeOmitsAnEmptyCatalog is D1156's byte-for-byte promise, and this
// task's red step: a project that names no catalog — and one whose config
// writes an empty one, which is the same project — publishes no image_catalog
// member at all, so the node of such a project is what it was before the key
// existed and no teammate of it meets the key's skew. The key's cost is the
// whole reason: an older CLI refuses a node that carries it
// (refuseUnknownPolicyKeys, TestOlderCLIRefusesANodeCarryingImageCatalog).
func TestPolicyNodeOmitsAnEmptyCatalog(t *testing.T) {
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	if raw := nodeJSON(t, f, projectpolicy.PathShared()); strings.Contains(raw, "image_catalog") {
		t.Errorf("a project that names no catalog published a member:\n%s", raw)
	}
	// A written empty catalog is that same project: it is how a published
	// catalog is taken away on purpose, and it must not leave the member
	// behind — an empty block is still a key an older CLI refuses.
	g := newProjectFixture(t, "image_catalog: { entries: [] }\n")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	if raw := nodeJSON(t, g, projectpolicy.PathShared()); strings.Contains(raw, "image_catalog") {
		t.Errorf("a config that writes an empty catalog published a member:\n%s", raw)
	}
	if p := publishedPolicy(t, g); p.ImageCatalog != nil {
		t.Errorf("ImageCatalog = %+v, want none", p.ImageCatalog)
	}
}

// TestPolicyNodeCarriesTheCatalogWhenSet: the operator's catalog reaches the
// node whole — every entry, its tags, its source and the default marker — so
// the run and the build read the definitions the file states and nothing else.
func TestPolicyNodeCarriesTheCatalogWhenSet(t *testing.T) {
	f := newProjectFixture(t, catalogConfigYAML)
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	p := publishedPolicy(t, f)
	if p.ImageCatalog == nil || len(p.ImageCatalog.Entries) != 2 {
		t.Fatalf("ImageCatalog = %+v, want the config's two entries", p.ImageCatalog)
	}
	var names []string
	for _, e := range p.ImageCatalog.Entries {
		names = append(names, e.Name)
	}
	if want := []string{"universal", "rust"}; !slices.Equal(names, want) {
		t.Errorf("entries = %v, want %v in the order the file writes them", names, want)
	}
	universal := p.ImageCatalog.Entries[0]
	if !universal.Default {
		t.Errorf("the default marker did not reach the node: %+v", universal)
	}
	if want := []string{"node"}; !slices.Equal(universal.Tags, want) {
		t.Errorf("tags = %v, want %v", universal.Tags, want)
	}
	if universal.Source.Image != "ghcr.io/acme/agent:1.2" {
		t.Errorf("source = %+v, want the prebuilt reference the file names", universal.Source)
	}
	rust := p.ImageCatalog.Entries[1]
	if rust.Source.Path != "docker/agent.Dockerfile" || rust.Source.Repo != "acme/other" {
		t.Errorf("source = %+v, want the repo and path the file names", rust.Source)
	}
	if raw := nodeJSON(t, f, projectpolicy.PathShared()); !strings.Contains(raw, `"image_catalog"`) {
		t.Errorf("the node does not carry the catalog:\n%s", raw)
	}
}

// TestOlderCLIRefusesANodeCarryingImageCatalog is the skew D1156 states, as the
// CLI's half of it: a launcher older than the key models every top-level key
// but image_catalog, so a node that carries one is a node it does not know, and
// every CLI reader refuses such a node rather than acting on a half-read policy
// (P8 leaves the tolerance to the runner, which enforces every value it
// understands and logs the rest). The reader that predates the key is
// projectpolicy's own test (TestImageCatalogIsUnknownToAReaderThatPredatesIt,
// which decodes one node twice); what is pinned here is the refusal that reader
// reaches, because a reader that stopped refusing unknown keys would make the
// skew note false while the node still carried a key it does not read.
func TestOlderCLIRefusesANodeCarryingImageCatalog(t *testing.T) {
	older := &projectpolicy.Policy{Version: projectpolicy.Version, UnknownKeys: []string{"image_catalog"}}
	err := refuseUnknownPolicyKeys(older)
	if err == nil {
		t.Fatal("a launcher of an older CLI accepted a node carrying image_catalog")
	}
	if got := ExitCode(err); got != ExitUserError {
		t.Errorf("exit %d, want %d: the operator can fix this, so it is not a crash", got, ExitUserError)
	}
	for _, want := range []string{"image_catalog", projectpolicy.PathShared(), "does not know", "fugaro update"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	// The same reader on a node it does know refuses nothing: the refusal is
	// about the key nobody reads and not about the catalog.
	if err := refuseUnknownPolicyKeys(&projectpolicy.Policy{Version: projectpolicy.Version}); err != nil {
		t.Errorf("a node carrying no unknown key was refused: %v", err)
	}
}

// TestImageCatalogGateIsNotThePriceCatalogGate is F14: catalogSince and
// catalogShipped are the *price* catalog's — a route's require_zdr and
// price_policy — and the image catalog's gate is spelled apart from them
// (D1156). A release can ship one catalog and not the other, so a reader that
// spelled the two with one name, or that flipped one flag for both, would judge
// an image document by the price catalog's knowledge and the other way round.
func TestImageCatalogGateIsNotThePriceCatalogGate(t *testing.T) {
	if imageCatalogSince == catalogSince {
		t.Errorf("imageCatalogSince = catalogSince = %q: one constant for two catalogs, so a release can no longer ship one of them without judging the other", imageCatalogSince)
	}
	if imageCatalogSince != "0.17.0" {
		t.Errorf("imageCatalogSince = %q, want 0.17.0, the release these build tasks ship in (D1158)", imageCatalogSince)
	}
	// The two gates are two documents' states, and only the image catalog's
	// moves for this work: a release that publishes image_catalog flips
	// imageCatalogShipped and leaves catalogShipped where it is. What must not
	// happen here is shipping this key by flipping the price catalog's instead.
	if !imageCatalogShipped {
		t.Errorf("imageCatalogShipped is false although imageCatalogSince names %q, a tagged release: readiness holds back every job a catalog entry gives its image to (issue 1659)", imageCatalogSince)
	}
	if catalogShipped {
		t.Errorf("catalogShipped is true while catalogSince still names %q: the price catalog's gate moved with the image catalog's", catalogSince)
	}
}

// TestUpdatePublishesTheCatalogWithNoSecondCommand: the catalog reaches the
// node through the one command the user runs (the HARD RULE), the policy stage
// of `fugaro update` — the same stage that publishes verifier_accounts — and
// not through a second command beside it. The stage has nothing left to do
// once it has run, which is what "one command is the whole upgrade" means for a
// key that arrives inside it.
func TestUpdatePublishesTheCatalogWithNoSecondCommand(t *testing.T) {
	f := newProjectFixture(t, catalogConfigYAML)
	f.db.Set(projectpolicy.PathShared(), staleBudgetNode())
	h := newMigrationRig(t, f, &initOptions{})

	st, err := h.s.Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != initflow.Todo {
		t.Fatalf("the update's policy stage says %s %q, want a pending publish", st.State, st.Detail)
	}
	if _, err := h.apply(t, true); err != nil {
		t.Fatal(err)
	}
	p := publishedPolicy(t, f)
	if p.ImageCatalog == nil || len(p.ImageCatalog.Entries) != 2 {
		t.Fatalf("ImageCatalog = %+v, want the config's two entries", p.ImageCatalog)
	}
	h.s.forget()
	st, err = h.s.Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != initflow.Done {
		t.Errorf("the stage has %s %q after the update: publishing a catalog asks for no second command", st.State, st.Detail)
	}
}

// TestUpdatePublishesACatalogOnlyChange: the node already agrees with the config
// in everything except the catalog, so the only reason the stage has anything to
// publish is the catalog itself. The test above passes through a stale budget
// node and would pass without the catalog being compared at all.
func TestUpdatePublishesACatalogOnlyChange(t *testing.T) {
	f := newProjectFixture(t, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	if p := publishedPolicy(t, f); p.ImageCatalog != nil {
		t.Fatalf("the first publish carries a catalog: %+v", p.ImageCatalog)
	}
	// The operator then adds the catalog to the file and nothing else: the node
	// differs from the config only in that member.
	cfg := readFileString(t, f.path)
	if err := os.WriteFile(f.path, []byte(cfg+"\n"+catalogConfigYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newMigrationRig(t, f, &initOptions{})

	st, err := h.s.Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != initflow.Todo {
		t.Fatalf("the update's policy stage says %s %q, want a pending publish: a node that differs only in the catalog is not up to date", st.State, st.Detail)
	}
	if _, err := h.apply(t, true); err != nil {
		t.Fatal(err)
	}
	if p := publishedPolicy(t, f); p.ImageCatalog == nil || len(p.ImageCatalog.Entries) != 2 {
		t.Fatalf("ImageCatalog = %+v, want the config's two entries", p.ImageCatalog)
	}
}

// TestImageCatalogIsKeptWhenTheConfigIsSilent is the keep rule's publish half
// (D1140, D1156), and the reason this task is a SEC one: a machine whose file
// says nothing about the catalog — a teammate's, a CI box's, an operator's
// second laptop — must not republish the node without it, which would leave
// every repository that names an entry with nothing to run. Deleting the key is
// therefore not how a catalog is taken away; the test below is the spelling
// that is.
func TestImageCatalogIsKeptWhenTheConfigIsSilent(t *testing.T) {
	f := newProjectFixture(t, catalogConfigYAML)
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	rewriteProjectConfig(t, f, "")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	p := publishedPolicy(t, f)
	if p.ImageCatalog == nil || len(p.ImageCatalog.Entries) != 2 {
		t.Fatalf("ImageCatalog = %+v, want the published catalog kept", p.ImageCatalog)
	}
	if p.ImageCatalog.Entries[0].Name != "universal" {
		t.Errorf("entries[0] = %+v, want the entry the node already carried", p.ImageCatalog.Entries[0])
	}
}

// TestImageCatalogIsDroppedOnAnExplicitEmptyCatalog: a written catalog with no
// entry is how a published one is taken away on purpose, and taking it away
// takes the member away too — the node an operator clears is byte for byte the
// node of a project that never named a catalog, so no launcher of an older CLI
// meets the key's skew for a catalog nobody has.
func TestImageCatalogIsDroppedOnAnExplicitEmptyCatalog(t *testing.T) {
	f := newProjectFixture(t, catalogConfigYAML)
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	rewriteProjectConfig(t, f, "image_catalog: { entries: [] }\n")
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	if p := publishedPolicy(t, f); p.ImageCatalog != nil {
		t.Errorf("ImageCatalog = %+v, want none after a written empty catalog", p.ImageCatalog)
	}
	if raw := nodeJSON(t, f, projectpolicy.PathShared()); strings.Contains(raw, "image_catalog") {
		t.Errorf("the node still carries the member:\n%s", raw)
	}
}
