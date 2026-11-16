package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
)

// secretMountDef is a definition that asks a build for a secret of its own: the
// shape an entry published by an earlier release carries in the bucket, and the
// shape this release refuses to write (D2311).
const secretMountDef = "ARG FUGARO_BASE\nFROM ${FUGARO_BASE}\nRUN --mount=type=secret,id=git-credentials git clone \"$REPO_URL\" /work/repo\n"

// TestDoctorReportsAnEntryThatMountsASecret: an entry already published in the
// project's bucket whose definition asks for a secret of its own is reported by
// doctor, once, and no line of doctor's offers a rebuild of it (B9, D2311).
//
// The report is doctor's own check and not the catalog image check's
// (image-catalog-<workflow>): that one's sentences are about a build, and every
// one of them would send the person to a build that this release refuses to
// submit (image_check_catalog.go). So the entry is named by catalog-secret-<entry>
// alone, and the entry's name is in the ID because the fact belongs to the
// definition and not to a workflow.
//
// "Once" is the whole point of the shape: the fact is one row about one
// definition, so two workflows that run the entry are told once between them —
// and it is the opposite of the check beside it, which is emitted per workflow.
func TestDoctorReportsAnEntryThatMountsASecret(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	// Both workflows run the catalog's one entry: one pins it, the other needs
	// the tag it covers (doctorCatalogFixture's entry is tagged node).
	dir := doctorCatalogCheckout(t, f, minimalAnchored+"git: { provider: github }\nworkflows:\n"+
		"  app: { image_name: go, commands: { build: sh build.sh, test: sh test.sh } }\n"+
		"  other: { needs: [node], commands: { build: sh build.sh, test: sh test.sh } }\n")
	lc := fileEnv(t, f).lc
	ctx := context.Background()
	key, err := config.CatalogDockerfileKey("go")
	if err != nil {
		t.Fatal(err)
	}
	writeBucketFile(t, f, key, secretMountDef)

	checks := doctorLayerChecks(ctx, lc, dir, resolvedLayerFixture(t, ctx, lc, dir))
	c := layerCheck(checks, "catalog-secret-go")
	if c == nil {
		t.Fatalf("doctor reported no entry that mounts a secret: %+v", checks)
	}
	if c.Severity != "warning" {
		t.Errorf("severity = %q, want warning (a mounted secret fails --strict)", c.Severity)
	}
	// The line is named and the line's text is not: the text is what carries
	// the secret (catalog.SecretRefusal).
	if !strings.Contains(c.Problem, "line 3") {
		t.Errorf("problem = %q, want the line the mount is written on", c.Problem)
	}
	if !strings.Contains(c.Problem, "the clone step supplies the checkout") {
		t.Errorf("problem = %q, want what supplies the checkout", c.Problem)
	}
	if strings.Contains(c.Problem, "git-credentials") || strings.Contains(c.Problem, "--mount") {
		t.Errorf("problem = %q, want no echo of the definition's own text", c.Problem)
	}
	if !strings.Contains(c.Fix, "fugaro image build go") {
		t.Errorf("fix = %q, want the build that publishes the entry again", c.Fix)
	}
	// One row for the entry, however many workflows run it, and no catalog
	// image check beside it: that check's fix would name a rebuild the daily
	// check refuses to submit.
	n := 0
	for _, got := range checks {
		if strings.HasPrefix(got.ID, "catalog-secret-") {
			n++
		}
		if strings.HasPrefix(got.ID, "image-catalog-") {
			t.Errorf("%s says something about a build of the entry: %+v", got.ID, got)
		}
	}
	if n != 1 {
		t.Errorf("catalog-secret checks = %d, want one for the entry", n)
	}

	// The same entry, republished with no secret in it, says nothing: the
	// check is about the definition and not about the entry's name.
	writeBucketFile(t, f, key, "ARG FUGARO_BASE\nFROM ${FUGARO_BASE}\nRUN true\n")
	if c := layerCheck(doctorLayerChecks(ctx, lc, dir, resolvedLayerFixture(t, ctx, lc, dir)), "catalog-secret-go"); c != nil {
		t.Fatalf("a definition that asks for no secret was reported: %+v", c)
	}
}

// TestUpdateWarnsOnceAboutAnEntryThatMountsASecret: the update's own line
// about a published entry that asks for a secret of its own (B9, D2311), read
// through the same fact doctor reports — the object in the bucket, which is
// what a build of the entry reads.
//
// "Once" is the run's guard and not the repository's: the fact is the entry's,
// and a converge that reaches several repositories running it says it once.
// A repository whose workflows run no catalog entry says nothing at all, and
// reaches no bucket for it.
func TestUpdateWarnsOnceAboutAnEntryThatMountsASecret(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	lc := fileEnv(t, f).lc
	ctx := context.Background()
	key, err := config.CatalogDockerfileKey("go")
	if err != nil {
		t.Fatal(err)
	}
	writeBucketFile(t, f, key, secretMountDef)

	var out strings.Builder
	r := &initRun{w: &out}
	// The same entry under two workflow keys, the way an account or a shared
	// job's spec repeats one repository's workflows, and then the same entry
	// again: what the guard collapses is a repeated entry, not a repeated key.
	spec := infra.RepoSpec{Workflows: map[string]infra.WorkflowSpec{
		"app":   {Name: "app", CatalogEntry: "go"},
		"app_b": {Name: "app", CatalogEntry: "go"},
	}}
	r.warnCatalogSecrets(ctx, lc, spec)
	r.warnCatalogSecrets(ctx, lc, spec)

	// A repository that runs no catalog entry warns about none, and is not
	// what makes the bucket be read at all.
	var none strings.Builder
	(&initRun{w: &none}).warnCatalogSecrets(ctx, lc, infra.RepoSpec{Workflows: map[string]infra.WorkflowSpec{
		"app": {Name: "app"},
	}})
	if none.Len() != 0 {
		t.Errorf("a repository with no catalog entry was warned about: %q", none.String())
	}

	got := out.String()
	if n := strings.Count(got, "warning: "); n != 1 {
		t.Fatalf("the update warned %d times, want once:\n%s", n, got)
	}
	for _, want := range []string{"the catalog entry go", "line 3", "the clone step supplies the checkout",
		"image build go", "its build is not submitted"} {
		if !strings.Contains(got, want) {
			t.Errorf("the update printed %q, want %q", got, want)
		}
	}
	if len(r.res.Warnings) != 1 {
		t.Errorf("the run's warnings are %v, want the one line", r.res.Warnings)
	}

	// Republished without the mount, the same entry says nothing: the fact is
	// the definition's and not the entry's name.
	writeBucketFile(t, f, key, "ARG FUGARO_BASE\nFROM ${FUGARO_BASE}\nRUN true\n")
	var quiet strings.Builder
	(&initRun{w: &quiet}).warnCatalogSecrets(ctx, lc, spec)
	if quiet.Len() != 0 {
		t.Errorf("an entry republished with no secret in it was warned about: %q", quiet.String())
	}
}

// TestDoctorCatalogSecretLeavesOtherMountsAlone: the check reads the entry's
// own request and nothing else. A definition whose RUN mounts a cache, a bind
// or a tmpfs carries no credential, and the dependency warm-up mount the base
// image itself writes is a cache: refusing it here would report every
// definition the images skill generates.
func TestDoctorCatalogSecretLeavesOtherMountsAlone(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	dir := doctorCatalogCheckout(t, f, minimalAnchored+"git: { provider: github }\nworkflows:\n"+
		"  app: { image_name: go, commands: { build: sh build.sh, test: sh test.sh } }\n")
	lc := fileEnv(t, f).lc
	ctx := context.Background()
	key, err := config.CatalogDockerfileKey("go")
	if err != nil {
		t.Fatal(err)
	}
	writeBucketFile(t, f, key, "ARG FUGARO_BASE\nFROM ${FUGARO_BASE}\n"+
		"RUN --mount=type=cache,id=fugaro-mise,target=/home/fugaro/.cache/mise --mount=type=bind,source=/a,target=/b true\n")

	for _, got := range doctorLayerChecks(ctx, lc, dir, resolvedLayerFixture(t, ctx, lc, dir)) {
		if strings.HasPrefix(got.ID, "catalog-secret-") {
			t.Fatalf("a mount that carries no credential was reported: %+v", got)
		}
	}
}
