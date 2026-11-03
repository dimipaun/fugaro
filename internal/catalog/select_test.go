package catalog

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/config"
)

// entry is one catalog entry of the fixtures, in the shape PIC1's type
// takes: a name, the tags, and the default marker.
func entry(name string, tags string, def bool) config.Entry {
	e := config.Entry{Name: name, Default: def, Source: config.Source{Kind: config.SourceImage, Image: "ghcr.io/acme/" + name + ":1.0"}}
	if tags != "" {
		e.Tags = strings.Fields(tags)
	}
	return e
}

// testCatalog is reconciliation §3.3's own example catalog: backend is the
// near miss that covers node, postgres and redis but not firebase-emulator,
// frontend covers node alone and data covers postgres and redis.
func testCatalog() config.Catalog {
	return config.Catalog{Entries: []config.Entry{
		entry("backend", "node postgres redis", false),
		entry("frontend", "node", false),
		entry("data", "postgres redis", false),
	}}
}

// job is one repository's selection question.
func job(cat config.Catalog, backendName Backend, r Repo) Job {
	return Job{Catalog: cat, Backend: backendName, Repos: []Repo{r}}
}

// TestChooseTakesTheFewestCoveringTags is the red step of PIC5: with no
// selection in the tree there is nothing to choose with at all.
func TestChooseTakesTheFewestCoveringTags(t *testing.T) {
	cat := config.Catalog{Entries: []config.Entry{
		entry("universal", "node postgres redis firebase-emulator", false),
		entry("narrow", "node postgres", false),
	}}
	c, err := Choose(job(cat, CloudRun, Repo{Slug: "acme/api", Needs: []string{"node", "postgres"}}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Entry.Name != "narrow" {
		t.Fatalf("chose %q, want narrow: a candidate covers the needed set iff every needed tag is one of its tags, and the fewest covering tags wins", c.Entry.Name)
	}
}

func TestChooseTiesToTheDefault(t *testing.T) {
	cat := config.Catalog{Entries: []config.Entry{
		entry("alpha", "node postgres", false),
		entry("beta", "postgres node", true),
	}}
	c, err := Choose(job(cat, CloudRun, Repo{Slug: "acme/api", Needs: []string{"node"}}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Entry.Name != "beta" {
		t.Fatalf("chose %q, want beta: on a tie in tag count the entry marked default wins", c.Entry.Name)
	}
}

func TestChooseTiesByName(t *testing.T) {
	cat := config.Catalog{Entries: []config.Entry{
		entry("zulu", "node", false),
		entry("alpha", "node", false),
	}}
	c, err := Choose(job(cat, CloudRun, Repo{Slug: "acme/api", Needs: []string{"node"}}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Entry.Name != "alpha" {
		t.Fatalf("chose %q, want alpha: with no default the lexicographically first name wins", c.Entry.Name)
	}
}

func TestChooseIsOrderIndependent(t *testing.T) {
	cat := testCatalog()
	want, err := Choose(job(cat, CloudRun, Repo{Slug: "acme/api", Needs: []string{"node", "postgres", "redis"}}))
	if err != nil {
		t.Fatal(err)
	}
	shuffled := config.Catalog{Entries: []config.Entry{cat.Entries[2], cat.Entries[0], cat.Entries[1]}}
	got, err := Choose(job(shuffled, CloudRun, Repo{Slug: "acme/api", Needs: []string{"redis", "node", "postgres"}}))
	if err != nil {
		t.Fatal(err)
	}
	if got.Entry.Name != want.Entry.Name {
		t.Fatalf("catalog order changed the choice: %q against %q; the order is total and depends on no catalog order", got.Entry.Name, want.Entry.Name)
	}
}

// TestNoCoveringEntryRefusesBeforeTheJobStarts is the second half of PIC5's
// red step: nothing refuses today, because nothing chooses.
func TestNoCoveringEntryRefusesBeforeTheJobStarts(t *testing.T) {
	_, err := Choose(job(testCatalog(), CloudRun, Repo{Slug: "acme/api", Needs: []string{"postgres", "redis", "firebase-emulator"}}))
	if err == nil {
		t.Fatal("a needed set no entry covers was accepted")
	}
	want := "no image in this project's catalog covers the tags this job needs: [postgres redis firebase-emulator]\n" +
		"the catalog's images cover: backend [node postgres redis], frontend [node], data [postgres redis]\n" +
		"add an entry that covers the missing tags (fugaro image add <name> --tags postgres redis firebase-emulator), or add a \"universal\" entry that covers every tag this project uses"
	if err.Error() != want {
		t.Fatalf("refusal =\n%s\n\nwant\n%s", err.Error(), want)
	}
}

func TestEmptyNeededSetTakesTheDefaultNotTheFewestTags(t *testing.T) {
	cat := config.Catalog{Entries: []config.Entry{
		entry("narrow", "node", false),
		entry("universal", "node postgres redis", true),
	}}
	c, err := Choose(job(cat, CloudRun, Repo{Slug: "acme/api"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Entry.Name != "universal" {
		t.Fatalf("chose %q, want universal: a repository that names nothing asked for nothing, so the default wins and the tag counts are never compared", c.Entry.Name)
	}
}

func TestPinnedImageThatDoesNotCoverIsWarnedAndUsed(t *testing.T) {
	cat := config.Catalog{Entries: []config.Entry{
		entry("frontend", "node", false),
		entry("universal", "node postgres redis", true),
	}}
	c, err := Choose(job(cat, CloudRun, Repo{Slug: "acme/api", Pin: "frontend", Needs: []string{"node", "postgres"}}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Entry.Name != "frontend" || !c.Pinned {
		t.Fatalf("chose %+v, want the pinned frontend: a pin is not the algorithm and is used as it is", c)
	}
	for _, want := range []string{"frontend", "postgres"} {
		if !strings.Contains(c.Warn, want) {
			t.Errorf("the warning %q does not name %q", c.Warn, want)
		}
	}
}

func TestPinnedEntryTheProjectDoesNotHaveIsRefused(t *testing.T) {
	_, err := Choose(job(testCatalog(), CloudRun, Repo{Slug: "acme/api", Pin: "universal"}))
	if err == nil {
		t.Fatal("a pin naming no entry of the catalog was accepted")
	}
	for _, want := range []string{"universal", "backend"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %q", err.Error(), want)
		}
	}
}

func TestDockerTagOnCloudRunIsRefusedBeforeTheJobStarts(t *testing.T) {
	cat := config.Catalog{Entries: []config.Entry{
		entry("universal", "node postgres docker", true),
	}}
	_, err := Choose(job(cat, CloudRun, Repo{Slug: "acme/api", Needs: []string{"node", "docker"}}))
	if err == nil {
		t.Fatal("a needed docker tag on Cloud Run was accepted")
	}
	want := "this job's image is tagged docker, which needs a VM backend: Cloud Run gen-2 has no Docker daemon\n" +
		"run this job on the Batch/VM backend, or choose an image that does not carry the docker tag"
	if err.Error() != want {
		t.Fatalf("refusal =\n%s\n\nwant\n%s", err.Error(), want)
	}
}

func TestDockerTagOnCloudRunIsNeverAChosenCandidate(t *testing.T) {
	cat := config.Catalog{Entries: []config.Entry{
		entry("tiny", "node docker", false),
		entry("big", "node postgres redis", false),
	}}
	c, err := Choose(job(cat, CloudRun, Repo{Slug: "acme/api", Needs: []string{"node"}}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Entry.Name != "big" {
		t.Fatalf("chose %q, want big: on Cloud Run every docker-tagged entry is left out of the candidates, whatever its tag count", c.Entry.Name)
	}
}

func TestDockerTagOnCloudRunIsRefusedWhenPinned(t *testing.T) {
	cat := config.Catalog{Entries: []config.Entry{entry("heavy", "node docker", false)}}
	_, err := Choose(job(cat, CloudRun, Repo{Slug: "acme/api", Pin: "heavy"}))
	if err == nil {
		t.Fatal("a pinned docker-tagged entry on Cloud Run was accepted: the backend has no daemon, and the tag is a claim about the job's backend")
	}
}

func TestDockerTagOnTheVMBackendIsAllowed(t *testing.T) {
	cat := config.Catalog{Entries: []config.Entry{
		entry("tiny", "node docker", false),
		entry("big", "node postgres redis", false),
	}}
	c, err := Choose(job(cat, Batch, Repo{Slug: "acme/api", Needs: []string{"node", "docker"}}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Entry.Name != "tiny" {
		t.Fatalf("chose %q, want tiny: a VM job keeps the whole catalog, so a docker-tagged entry is an ordinary candidate there", c.Entry.Name)
	}
}

func TestNeededIsTheUnionOverTheRepositories(t *testing.T) {
	got := Needed([]Repo{
		{Slug: "acme/api", Needs: []string{"node", "postgres"}},
		{Slug: "acme/web", Needs: []string{"postgres", "redis"}},
		{Slug: "acme/pinned", Pin: "universal", Needs: []string{"docker"}},
	})
	want := []string{"node", "postgres", "redis"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("Needed = %v, want %v: the needed set is the union over the repositories that do not pin", got, want)
	}
}

func TestCoversAllowsExtraTagsAndRefusesAMissingOne(t *testing.T) {
	if !Covers([]string{"node", "postgres", "redis"}, []string{"node"}) {
		t.Error("a candidate carrying extra tags does not cover a needed set it holds")
	}
	if Covers([]string{"node"}, []string{"node", "postgres"}) {
		t.Error("a candidate covers a needed set it does not hold")
	}
	if !Covers(nil, nil) {
		t.Error("an empty needed set is covered by every entry")
	}
}

// TestSingleRepoJobNeedsOnlyItsOwnTags is D1154's pin: the rule is written
// for the union over a job's repositories, and today the union has one
// member, so one repository's needs choose exactly what they choose alone.
func TestSingleRepoJobNeedsOnlyItsOwnTags(t *testing.T) {
	cat := config.Catalog{Entries: []config.Entry{
		entry("api", "node postgres", false),
		entry("web", "node", false),
	}}
	c, err := Choose(job(cat, CloudRun, Repo{Slug: "acme/web", Needs: []string{"node"}}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Entry.Name != "web" {
		t.Fatalf("chose %q, want web: the single member's tags are the whole needed set", c.Entry.Name)
	}
}

// TestBackendNamesAreTheBackendPackagesAnswers: the selection's own Backend
// values must be the names internal/backend uses, because a launcher casts
// the backend it selected into one of these: a drifted name would read as a
// backend with no rule and let a docker-tagged job start on Cloud Run.
func TestBackendNamesAreTheBackendPackagesAnswers(t *testing.T) {
	if string(CloudRun) != backend.CloudRun || string(Batch) != backend.CloudBatch {
		t.Fatalf("the selection's backends are %q and %q, but internal/backend answers %q and %q", CloudRun, Batch, backend.CloudRun, backend.CloudBatch)
	}
}

func TestAnUnknownBackendHasNoDockerDaemon(t *testing.T) {
	cat := config.Catalog{Entries: []config.Entry{entry("tiny", "node docker", false)}}
	if _, err := Choose(job(cat, Backend(""), Repo{Slug: "acme/api", Needs: []string{"docker"}})); err == nil {
		t.Fatal("a backend the rule does not know was treated as if it had a Docker daemon")
	}
}

// TestAppliesIsTheJobsTheCatalogGivesAnImageTo pins which workflows the
// catalog speaks for at all, because two callers ask it and each of them
// refuses on the answer: the deploy-time job spec, and the launcher judging
// the same job's image against the backend it picks. A workflow one of them
// judged by the catalog and the other did not would be refused for an entry it
// does not run, or asked for a Docker socket it never needed.
//
// Mutation (drop the override arm, or the clone/shared arm): the launcher
// judges a job the deploy gave the repository's own image — and a catalog
// whose default is docker-tagged then refuses that launch on Cloud Run, or
// asks a VM installation with no gid recorded for a socket nobody needs.
func TestAppliesIsTheJobsTheCatalogGivesAnImageTo(t *testing.T) {
	// defaulted marks one entry as the project default, which is what a
	// repository that names nothing needs to be the catalog's at all.
	defaulted := testCatalog()
	defaulted.Entries[1].Default = true
	override := config.Workflow{Image: config.Image{Apt: []string{"jq"}}}
	baked := config.Workflow{Checkout: config.CheckoutBaked}

	for name, tt := range map[string]struct {
		cat  config.Catalog
		w    config.Workflow
		want bool
	}{
		"a repository that needs a tag":               {testCatalog(), config.Workflow{Checkout: config.CheckoutBaked, Needs: []string{"node"}}, true},
		"a repository that pins an entry":             {testCatalog(), config.Workflow{Checkout: config.CheckoutBaked, ImageName: "backend"}, true},
		"a repository that names nothing, a default":  {defaulted, baked, true},
		"a repository that names nothing, no default": {testCatalog(), baked, false},
		"its own image definition":                    {testCatalog(), override, false},
		"its own Dockerfile": {
			testCatalog(),
			config.Workflow{Checkout: config.CheckoutBaked, Dockerfile: "docker/Dockerfile"},
			false,
		},
		"a clone workflow":  {testCatalog(), config.Workflow{Checkout: config.CheckoutClone, Needs: []string{"node"}}, false},
		"a shared workflow": {testCatalog(), config.Workflow{Checkout: config.CheckoutShared, Needs: []string{"node"}}, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := Applies(tt.cat, tt.w); got != tt.want {
				t.Fatalf("Applies = %v, want %v", got, tt.want)
			}
		})
	}
}
