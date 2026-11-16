package catalog

import (
	"errors"
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

// TestNoCoveringEntryListsOnlyTrueNearMisses: §3.3's first message lists the
// whole catalog, and on Cloud Run that includes the entries the docker rule
// took out of the candidate set (D1149) — an entry that covers the needed tags
// and would otherwise read as a near miss, telling a person to add an image
// they already have. Each one is marked with the reason it was not a
// candidate, and on the VM backend, where the rule takes nothing out, nothing
// is marked.
//
// Mutation (drop the mark, or mark on every backend): the Cloud Run case lists
// dk as an ordinary near miss, or the VM case claims a rule the VM does not
// have.
func TestNoCoveringEntryListsOnlyTrueNearMisses(t *testing.T) {
	cat := config.Catalog{Entries: []config.Entry{entry("dk", "node docker", false)}}
	_, err := Choose(job(cat, CloudRun, Repo{Slug: "acme/api", Needs: []string{"node"}}))
	if err == nil {
		t.Fatal("a needed set the only entry is left out of was accepted on Cloud Run")
	}
	want := "the catalog's images cover: dk [node docker] (docker: VM backend only)"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("the Cloud Run refusal reads\n%s\n\nwant it to carry %q: dk covers the tags this job needs and is not a candidate, and saying only the second half sends the reader to add the image they have",
			err.Error(), want)
	}
	// The same entry on the VM backend, where it is an ordinary candidate and
	// this refusal is about its tags alone.
	_, err = Choose(job(cat, Batch, Repo{Slug: "acme/api", Needs: []string{"node", "firebase-emulator"}}))
	if err == nil {
		t.Fatal("a needed set no entry covers was accepted on the VM backend")
	}
	if want := "the catalog's images cover: dk [node docker]\n"; !strings.Contains(err.Error()+"\n", want) {
		t.Errorf("the VM refusal reads\n%s\n\nwant it to carry %q: the docker rule takes nothing out on a backend that has a daemon", err.Error(), want)
	}
	if strings.Contains(err.Error(), "VM backend only") {
		t.Errorf("the VM refusal marks an entry the docker rule kept:\n%s", err.Error())
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

// TestPinnedImageThatCoversEveryNeededTagWarnsNothing: the warning is for a pin
// that leaves a needed tag uncovered, so a pin that covers them all is used
// without one.
func TestPinnedImageThatCoversEveryNeededTagWarnsNothing(t *testing.T) {
	cat := config.Catalog{Entries: []config.Entry{
		entry("frontend", "node", false),
		entry("universal", "node postgres redis", true),
	}}
	c, err := Choose(job(cat, CloudRun, Repo{Slug: "acme/api", Pin: "universal", Needs: []string{"node", "postgres"}}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Entry.Name != "universal" || !c.Pinned {
		t.Fatalf("chose %+v, want the pinned universal", c)
	}
	if c.Warn != "" {
		t.Errorf("a pin that covers every needed tag warns %q", c.Warn)
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

// TestChooseLeavesTheCatalogsOwnEntriesAlone: the docker rule drops entries
// from the candidate set, and the set it drops them from is a copy. The
// caller's catalog is the effective one it read once and reuses — the deploy
// spec chooses for every workflow from it — so a DeleteFunc over that slice
// would shift the surviving entries into the front of the array it was given
// and leave the next workflow's choice reading a catalog that had been
// reordered.
func TestChooseLeavesTheCatalogsOwnEntriesAlone(t *testing.T) {
	cat := config.Catalog{Entries: []config.Entry{
		entry("tiny", "node docker", false),
		entry("big", "node postgres redis", false),
	}}
	before := strings.Join(catalogNames(cat.Entries), " ")
	if _, err := Choose(job(cat, CloudRun, Repo{Slug: "acme/api", Needs: []string{"node"}})); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(catalogNames(cat.Entries), " "); got != before {
		t.Fatalf("the catalog reads %q after a choice, want %q: the choice drops docker-tagged entries from a copy, never from the caller's catalog", got, before)
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

// mustImage is JobImage with the error treated as a failure: the selections
// these tests are about are the ones the resolver answers.
func mustImage(t *testing.T, req ImageRequest) Selection {
	t.Helper()
	sel, err := JobImage(req)
	if err != nil {
		t.Fatalf("JobImage(%+v) = %v", req, err)
	}
	return sel
}

// TestMayApplyIsWhichWorkflowsTheSelectionCouldSpeakFor pins the half of the
// question that reads no project at all, because doctor asks it for a project
// whose catalog it could not read: whether the image selection could speak for
// a workflow, without asking the catalog whether it marks a default or the map
// whether it names this repository. The two checkout kinds whose image is the
// installation's own are the ones it refuses; every other workflow is one the
// selection may speak for, whatever its keys say.
//
// Mutation (refuse a workflow that keeps its own image definition): doctor
// would read a repository outside the catalog's reach as one it is judging,
// where the whole point of the question in that state is that the node nobody
// read may still give it an entry (D1900).
func TestMayApplyIsWhichWorkflowsTheSelectionCouldSpeakFor(t *testing.T) {
	for name, tt := range map[string]struct {
		w    config.Workflow
		want bool
	}{
		"a repository that needs a tag":   {config.Workflow{Checkout: config.CheckoutBaked, Needs: []string{"node"}}, true},
		"a repository that pins an entry": {config.Workflow{Checkout: config.CheckoutBaked, ImageName: "backend"}, true},
		"a repository that names nothing": {config.Workflow{Checkout: config.CheckoutBaked}, true},
		"its own image definition":        {config.Workflow{Checkout: config.CheckoutBaked, Image: config.Image{Apt: []string{"jq"}}}, true},
		"its own Dockerfile":              {config.Workflow{Checkout: config.CheckoutBaked, Dockerfile: "docker/Dockerfile"}, true},
		"a clone workflow":                {config.Workflow{Checkout: config.CheckoutClone, Needs: []string{"node"}}, false},
		"a shared workflow":               {config.Workflow{Checkout: config.CheckoutShared, Needs: []string{"node"}}, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := MayApply(tt.w); got != tt.want {
				t.Fatalf("MayApply = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestJobImageIgnoresRepoLevelConfig is D1900's rule in the resolver: a
// repository's own image definition is not what its job runs while the project
// gives it an image, and every key that asked for one says so once.
//
// The frozen workflow is the tree's own per-repository shape (image.apt, a
// dockerfile, a rebuild: block), which is what the warning is read against: a
// key this resolver did not name would leave the person's `image:` block in
// their file meaning nothing, with no line anywhere saying so.
func TestJobImageIgnoresRepoLevelConfig(t *testing.T) {
	// The default is an entry this job can run, which is what makes the
	// workflow's own keys ignored rather than the default set aside: a project
	// whose default is a prebuilt image keeps the workflow's own image instead
	// (TestJobImageKeepsOwnImageWhenTheDefaultCannotRun).
	cat := config.Catalog{Entries: []config.Entry{
		{Name: "frontend", Default: true, Tags: []string{"node"}, Source: config.Source{Kind: config.SourceDockerfile, Repo: "acme/images", Path: "frontend/Dockerfile"}},
		entry("frontend", "node", false),
		entry("data", "postgres redis", false),
	}}
	w := config.Workflow{
		Checkout:   config.CheckoutBaked,
		Image:      config.Image{Apt: []string{"jq"}},
		Dockerfile: "docker/Dockerfile",
		Rebuild:    config.Rebuild{Check: "weekly"},
	}
	sel := mustImage(t, ImageRequest{Catalog: &cat, Workflow: w, Backend: CloudRun, Canonical: "acme/api"})
	if sel.Source != SourceCatalog || sel.Entry.Name != "frontend" {
		t.Fatalf("resolved %+v, want the project's default entry frontend: a repository's own image keys are ignored while the project gives the job an image (D1900)", sel)
	}
	want := []string{"image:", "dockerfile:", "rebuild:"}
	if len(sel.Warnings) != len(want) {
		t.Fatalf("warnings = %q, want one for each of %q", sel.Warnings, want)
	}
	for i, key := range want {
		if !strings.Contains(sel.Warnings[i], key) {
			t.Errorf("warning %d = %q, want it to name %s", i, sel.Warnings[i], key)
		}
	}
}

// TestJobImageKeepsOwnImageWhenTheDefaultCannotRun is D1900 read against the
// two defaults a job cannot run: D1191's prebuilt entry, which holds no
// checkout of the repository a baked image builds from, and D1149's
// docker-tagged entry on a backend with no daemon. A workflow that wrote an
// image definition of its own keeps running it, with a warning naming the key
// and the entry, because the alternative is refusing a configuration that
// deploys today — the manual config edit the HARD RULE on `fugaro update`
// rules out. A workflow that wrote no definition of its own has nothing of its
// own to keep and is left to the refusal, which names where its fix is.
func TestJobImageKeepsOwnImageWhenTheDefaultCannotRun(t *testing.T) {
	prebuilt := config.Catalog{Entries: []config.Entry{entry("standard", "", true)}}
	dockerTagged := config.Catalog{Entries: []config.Entry{{
		Name:    "standard",
		Default: true,
		Tags:    []string{DockerTag},
		Source:  config.Source{Kind: config.SourceInline, Inline: "FROM scratch\n"},
	}}}
	builtDefault := config.Catalog{Entries: []config.Entry{{
		Name:    "frontend",
		Default: true,
		Source:  config.Source{Kind: config.SourceDockerfile, Repo: "acme/images", Path: "frontend/Dockerfile"},
	}}}
	baked := func(w config.Workflow) config.Workflow {
		w.Checkout = config.CheckoutBaked
		return w
	}
	for name, tt := range map[string]struct {
		cat config.Catalog
		w   config.Workflow
		key string
		why string
	}{
		"dockerfile: against a prebuilt default": {
			cat: prebuilt,
			w:   baked(config.Workflow{Dockerfile: "docker/Dockerfile"}),
			key: "dockerfile:", why: "prebuilt",
		},
		"image: against a prebuilt default": {
			cat: prebuilt,
			w:   baked(config.Workflow{Image: config.Image{Apt: []string{"jq"}}}),
			key: "image:", why: "prebuilt",
		},
		"dockerfile: against a docker-tagged default": {
			cat: dockerTagged,
			w:   baked(config.Workflow{Dockerfile: "docker/Dockerfile"}),
			key: "dockerfile:", why: DockerTag,
		},
	} {
		t.Run(name, func(t *testing.T) {
			sel := mustImage(t, ImageRequest{Catalog: &tt.cat, Workflow: tt.w, Backend: CloudRun, Canonical: "acme/api"})
			if sel.Source != SourceRepo {
				t.Fatalf("resolved %+v, want the repository's own image: the default is an entry this job cannot run and the workflow wrote a definition of its own", sel)
			}
			if len(sel.Warnings) != 1 {
				t.Fatalf("warnings = %q, want the one the set-aside default carries", sel.Warnings)
			}
			for _, want := range []string{tt.key, "standard", tt.why} {
				if !strings.Contains(sel.Warnings[0], want) {
					t.Errorf("warning = %q, want it to name %s", sel.Warnings[0], want)
				}
			}
		})
	}

	// The controls. A workflow that wrote only rebuild: has no image of its own,
	// so it is not this shape and the prebuilt default stays its answer — which
	// the job-spec builder then refuses (D1191). A workflow that wrote nothing
	// at all met the docker-tagged default on Cloud Run before this fallback and
	// still does: the refusal is the resolver's own (D1149), and there is no
	// image of the job's own for the catalog's answer to be set aside in favour
	// of. A default this job *can* run stays the answer for a workflow that
	// wrote a definition, with D1900's warning for the key it set aside, on
	// Cloud Run and on the VM alike.
	sel := mustImage(t, ImageRequest{Catalog: &prebuilt, Workflow: baked(config.Workflow{Rebuild: config.Rebuild{Check: "weekly"}}), Backend: CloudRun, Canonical: "acme/api"})
	if sel.Source != SourceCatalog || sel.Entry.Name != "standard" {
		t.Fatalf("resolved %+v, want the prebuilt default standard: rebuild: alone is no image of the workflow's own to keep", sel)
	}
	if _, err := JobImage(ImageRequest{Catalog: &dockerTagged, Workflow: baked(config.Workflow{}), Backend: CloudRun, Canonical: "acme/api"}); err == nil {
		t.Fatal("a docker-tagged default was given to a workflow that wrote nothing of its own on Cloud Run")
	} else if !errors.Is(err, ErrDockerOnCloudRun) {
		t.Fatalf("refusal of the docker-tagged default = %v, want D1149's refusal", err)
	}
	for name, back := range map[string]Backend{"Cloud Run": CloudRun, "the VM": Batch} {
		t.Run(name, func(t *testing.T) {
			sel := mustImage(t, ImageRequest{Catalog: &builtDefault, Workflow: baked(config.Workflow{Dockerfile: "docker/Dockerfile"}), Backend: back, Canonical: "acme/api"})
			if sel.Source != SourceCatalog || sel.Entry.Name != "frontend" {
				t.Fatalf("resolved %+v, want the project's default frontend: it is an entry this job can run", sel)
			}
			if len(sel.Warnings) != 1 || !strings.Contains(sel.Warnings[0], "dockerfile:") {
				t.Fatalf("warnings = %q, want the ignored dockerfile: named once", sel.Warnings)
			}
		})
	}
	// The same catalogue on the VM: the docker-tagged default is runnable there
	// (the backend's container carries the host's socket), so it is the answer
	// and not a default set aside.
	sel = mustImage(t, ImageRequest{Catalog: &dockerTagged, Workflow: baked(config.Workflow{Dockerfile: "docker/Dockerfile"}), Backend: Batch, Canonical: "acme/api"})
	if sel.Source != SourceCatalog || sel.Entry.Name != "standard" {
		t.Fatalf("resolved %+v, want the docker-tagged default on the VM: the backend has a daemon", sel)
	}
}

// TestJobImageUsesImageMap is D1906's map in the resolver, and T1 reading (b)
// against it: a repository the project maps runs the entry it is mapped to,
// while a repository that states a reference of its own — a pin or a needed
// tag — is resolved by that reference and never by the map.
func TestJobImageUsesImageMap(t *testing.T) {
	cat := testCatalog()
	cat.Entries[1].Default = true // frontend, the default a mapped repo never reaches
	imap := map[string]string{"acme/api": "backend"}
	req := func(w config.Workflow) ImageRequest {
		return ImageRequest{Catalog: &cat, ImageMap: imap, Workflow: w, Backend: CloudRun, Canonical: "acme/api"}
	}
	sel := mustImage(t, req(config.Workflow{Checkout: config.CheckoutBaked, Image: config.Image{Apt: []string{"jq"}}}))
	if sel.Source != SourceCatalog || sel.Entry.Name != "backend" {
		t.Fatalf("resolved %+v, want the mapped entry backend", sel)
	}
	if len(sel.Warnings) != 1 || !strings.Contains(sel.Warnings[0], "image:") {
		t.Fatalf("warnings = %q, want the ignored image: named once", sel.Warnings)
	}
	// The map is keyed by canonical repository name: a checkout spelled with
	// capitals is the same repository's job.
	upper := req(config.Workflow{Checkout: config.CheckoutBaked})
	upper.Canonical = "Acme/API"
	if got := mustImage(t, upper).Entry.Name; got != "backend" {
		t.Fatalf("a repository spelled Acme/API resolved %q, want backend: the map is read under the repository's canonical name (ValidImageMap)", got)
	}
	// T1 (b): a reference of the repository's own wins over the map.
	if got := mustImage(t, req(config.Workflow{Checkout: config.CheckoutBaked, ImageName: "data"})).Entry.Name; got != "data" {
		t.Fatalf("a repository that pins data resolved %q, want data: a catalog reference beats the map (T1 b)", got)
	}
	if got := mustImage(t, req(config.Workflow{Checkout: config.CheckoutBaked, Needs: []string{"postgres", "redis"}})).Entry.Name; got != "data" {
		t.Fatalf("a repository that needs postgres and redis resolved %q, want data: needs: are a catalog reference and beat the map (T1 b)", got)
	}
	// A map that names an entry the catalog does not hold is refused, and the
	// sentence names the map: that is where the person's fix is.
	missing := req(config.Workflow{Checkout: config.CheckoutBaked})
	missing.ImageMap = map[string]string{"acme/api": "nowhere"}
	if _, err := JobImage(missing); err == nil {
		t.Fatal("a map entry no catalog holds was accepted")
	} else if !strings.Contains(err.Error(), "image_map") {
		t.Fatalf("the refusal of an unknown mapped entry is %q, want it to name the image_map", err)
	}
}

// TestJobImageNoDefaultFallsBack is O4 reading (b): a project that has not
// curated its catalog gives no entry to a repository that names nothing, and
// refusing there would stop `fugaro update` for every installation that has
// not been through the image skill. The repository keeps the image this
// project builds for it until a default exists, and nothing is warned about:
// no key was ignored, because no project image took its place.
func TestJobImageNoDefaultFallsBack(t *testing.T) {
	cat := testCatalog() // no entry marked default
	w := config.Workflow{Checkout: config.CheckoutBaked, Image: config.Image{Apt: []string{"jq"}}}
	sel := mustImage(t, ImageRequest{Catalog: &cat, Workflow: w, Backend: CloudRun, Canonical: "acme/api"})
	if sel.Source != SourceRepo {
		t.Fatalf("resolved %+v, want the repository's own image", sel)
	}
	if len(sel.Warnings) != 0 {
		t.Fatalf("warnings = %q, want none: no project image took the workflow's own away", sel.Warnings)
	}
	// A project with no catalog at all is the same answer, and so is one whose
	// repository names nothing while nothing maps it.
	for name, req := range map[string]ImageRequest{
		"no catalog":       {Workflow: w, Backend: CloudRun, Canonical: "acme/api"},
		"an empty catalog": {Catalog: &config.Catalog{}, Workflow: w, Backend: CloudRun, Canonical: "acme/api"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := mustImage(t, req); got.Source != SourceRepo {
				t.Fatalf("resolved %+v, want the repository's own image", got)
			}
		})
	}
}

// TestJobImageCheckoutKindsAreTheInstallationsImages: the two checkout kinds
// whose image is not a project image at all — the base image a clone job runs
// and the shared dependency layer a shared job runs — are answered before any
// of the project's configuration is read, so a catalog entry, a map or a
// default cannot move them.
func TestJobImageCheckoutKindsAreTheInstallationsImages(t *testing.T) {
	cat := testCatalog()
	cat.Entries[1].Default = true
	for name, tt := range map[string]struct {
		w    config.Workflow
		want Source
	}{
		"clone":  {config.Workflow{Checkout: config.CheckoutClone}, SourceBase},
		"shared": {config.Workflow{Checkout: config.CheckoutShared}, SourceShared},
	} {
		t.Run(name, func(t *testing.T) {
			sel := mustImage(t, ImageRequest{
				Catalog:   &cat,
				ImageMap:  map[string]string{"acme/api": "backend"},
				Workflow:  tt.w,
				Backend:   CloudRun,
				Canonical: "acme/api",
			})
			if sel.Source != tt.want || sel.Entry.Name != "" {
				t.Fatalf("resolved %+v, want source %s and no entry", sel, tt.want)
			}
		})
	}
}

// TestSelectionImageNamesTheImageOfItsSource: the four sources name four
// references, and the two the resolver must not know — the installation's base
// image and the place a catalog entry's image lives — come from the caller.
func TestSelectionImageNamesTheImageOfItsSource(t *testing.T) {
	im := Images{
		Base:    "base:1",
		Repo:    "registry/acme/api:latest",
		Catalog: func(e config.Entry) string { return "registry/catalog-" + e.Name + ":latest" },
	}
	for name, tt := range map[string]struct {
		sel  Selection
		want string
	}{
		"the base image":   {Selection{Source: SourceBase}, "base:1"},
		"the repository's": {Selection{Source: SourceRepo}, "registry/acme/api:latest"},
		"a shared layer":   {Selection{Source: SourceShared}, "registry/acme/api:latest"},
		"a catalog entry":  {Selection{Source: SourceCatalog, Entry: entry("frontend", "node", true)}, "registry/catalog-frontend:latest"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := tt.sel.Image(im); got != tt.want {
				t.Fatalf("Image = %q, want %q", got, tt.want)
			}
		})
	}
}
