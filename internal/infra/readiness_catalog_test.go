package infra

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

// catalogBlock is a project image catalog whose one entry is the default, so
// the webapp's workflows name nothing of their own and PIC5's catalogJobImage
// routes their jobs through the entry (reconciliation §3.1 step 3).
const catalogBlock = `
image_catalog:
  entries:
    - name: base
      tags: [node]
      default: true
      description: the everyday image
      source: { kind: inline, inline: "FROM scratch\n" }
`

// catalogReadiness is Readiness over a spec whose workflows run a catalog
// entry, with the shipped gate set the way the release that reads the key
// sets it: the entry's image is in the installation's shared registry, which
// is not the repository's own, so this is the one thing about it readiness
// cannot ask the repository.
func catalogReadiness(t *testing.T, f *cloud, spec RepoSpec, shipped bool) (RepoSpec, []Missing) {
	t.Helper()
	was := imageCatalogShipped
	imageCatalogShipped = shipped
	t.Cleanup(func() { imageCatalogShipped = was })
	_, ex, err := DiscoverRepo(context.Background(), f.c, spec)
	if err != nil {
		t.Fatal(err)
	}
	// The policy gate is a different question (D11) and this fixture carries
	// no Firebase backend, so the project has published no policy here.
	ex.PolicyExists = false
	got, missing, err := Readiness(context.Background(), f.c, spec, ex, false)
	if err != nil {
		t.Fatal(err)
	}
	return got, missing
}

// catalogSpec is the webapp with the catalog above, ready in every way but
// the entry's own image: its repository exists, its secrets have versions and
// its runs bucket is there.
func catalogSpec(t *testing.T) (*cloud, RepoSpec) {
	t.Helper()
	f := newCloud(t)
	spec, err := Repo(budgetInputs(t, catalogBlock))
	if err != nil {
		t.Fatal(err)
	}
	readyJob(t, f, spec)
	for name, ws := range spec.Workflows {
		if !strings.Contains(ws.Image, "/"+catalogImagePrefix+"base:latest") {
			t.Fatalf("workflow %s's image is %s, want the catalog entry's: the fixture must pin nothing of its own", name, ws.Image)
		}
	}
	return f, spec
}

// TestReadinessAcceptsABuiltCatalogImage: the entry's image is built when its
// build record is in the runs bucket or when the :latest tag the job pulls is
// in the installation's shared registry, and either one deploys the job. The
// question hasLatest asks (is <repository registry>/<pkg>:latest there?) is
// about a package this repository's build never writes, so asking it refused
// every job a catalog describes.
func TestReadinessAcceptsABuiltCatalogImage(t *testing.T) {
	for _, c := range []struct {
		name string
		seed func(t *testing.T, f *cloud, spec RepoSpec)
	}{
		{"the build record", func(t *testing.T, f *cloud, spec RepoSpec) {
			key, err := gcp.CatalogRecordKey(spec.Slug, "base")
			if err != nil {
				t.Fatal(err)
			}
			f.gcs.Put(spec.Installation.RunsBucket, key, []byte(`{"entry":"base"}`))
		}},
		{"the latest tag", func(t *testing.T, f *cloud, spec RepoSpec) {
			f.ar.AddRepository(spec.GCPProject, "us-east5", spec.Installation.SharedRegistry, nil)
			f.ar.SetTag(spec.GCPProject, "us-east5", spec.Installation.SharedRegistry, catalogImagePrefix+"base", "latest")
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, spec := catalogSpec(t)
			c.seed(t, f, spec)
			got, missing := catalogReadiness(t, f, spec, true)
			if slices.ContainsFunc(missing, func(m Missing) bool { return m.Kind == MissingImage }) {
				t.Fatalf("missing = %v, want the built entry's image to be no gate", missing)
			}
			for name, ws := range got.Workflows {
				if !ws.DeployJob {
					t.Errorf("workflow %s is not deployed: %v", name, missing)
				}
			}
		})
	}
}

// TestReadinessRefusesAnUnbuiltCatalogImage: an entry nothing has built is
// held back with the command that builds it, named for the entry and not for
// the repository: a catalog image is not this repository's own.
func TestReadinessRefusesAnUnbuiltCatalogImage(t *testing.T) {
	f, spec := catalogSpec(t)
	got, missing := catalogReadiness(t, f, spec, true)
	images := 0
	for _, m := range missing {
		if m.Kind != MissingImage {
			continue
		}
		images++
		for _, want := range []string{"base", "fugaro image build base", spec.Installation.SharedRegistry} {
			if !strings.Contains(m.Reason, want) {
				t.Errorf("the refusal %q lacks %q", m.Reason, want)
			}
		}
	}
	if images != len(got.Workflows) {
		t.Errorf("missing = %v, want one image gate per workflow", missing)
	}
	for name, ws := range got.Workflows {
		if ws.DeployJob {
			t.Errorf("workflow %s is deployed with an image nothing built", name)
		}
	}
}

// prebuiltRef is a prebuilt entry's image: the operator's own name and tag in
// the installation's shared registry, which isCatalogImage cannot recognise
// (it is not catalog-<entry>) and which is not the repository's own registry.
const prebuiltRef = "us-east5-docker.pkg.dev/proj/fugaro-shared-images-0123/team-image:1.0-latest"

// prebuiltCatalogBlock is a catalog whose one entry is the default, so the
// webapp's workflows name nothing of their own and the entry is what their
// jobs run. Its source is one this installation builds, because the two tests
// below do not build the state they read through Repo: readiness reads the mark
// a spec's image carries when a prebuilt entry gave it one (CatalogPrebuilt),
// and the deploy path now refuses a prebuilt entry on a baked workflow before
// it gets that far (D1191), so prebuiltSpec states the mark on the spec itself
// — the state a job deployed before that refusal reads as.
const prebuiltCatalogBlock = `
image_catalog:
  entries:
    - name: team
      tags: [jvm]
      default: true
      source: { kind: inline, inline: "FROM scratch\n" }
`

// prebuiltSpec is the webapp ready in every way but the entry's own image,
// with the workflows' specs marked as running the prebuilt reference above:
// the two fields workflowCatalogEntry and the image gate read. The mark is
// stated on a spec the catalog gave its entry to — the guard below is
// catalogSpec's, for the same reason: a workflow the catalog does not speak
// for would be marked as one it does, and the tests below would pass while
// covering a shape no repository has.
func prebuiltSpec(t *testing.T) (*cloud, RepoSpec) {
	t.Helper()
	f := newCloud(t)
	spec, err := Repo(budgetInputs(t, prebuiltCatalogBlock))
	if err != nil {
		t.Fatal(err)
	}
	for name, ws := range spec.Workflows {
		if !strings.Contains(ws.Image, "/"+catalogImagePrefix+"team:latest") {
			t.Fatalf("workflow %s's image is %s, want the catalog entry's: the fixture must take the entry first", name, ws.Image)
		}
		ws.Image = prebuiltRef
		ws.CatalogPrebuilt = true
		spec.Workflows[name] = ws
	}
	readyJob(t, f, spec)
	return f, spec
}

// TestReadinessDeploysAPrebuiltCatalogEntry: a prebuilt entry's job runs the
// reference the entry states, which no Fugaro build writes, so there is no
// build record or catalog :latest tag to find and the job is ready as written.
// Before, readiness took the reference for the repository's own derived tag
// and refused it with "is not <registry>/<package>:latest in the repository's
// registry", so no prebuilt entry could be deployed at all.
func TestReadinessDeploysAPrebuiltCatalogEntry(t *testing.T) {
	f, spec := prebuiltSpec(t)
	got, missing := catalogReadiness(t, f, spec, true)
	if slices.ContainsFunc(missing, func(m Missing) bool { return m.Kind == MissingImage }) {
		t.Fatalf("missing = %v, want a prebuilt entry's image to be no gate", missing)
	}
	for name, ws := range got.Workflows {
		if ws.Image != prebuiltRef || ws.CatalogEntry != "team" || !ws.CatalogPrebuilt {
			t.Errorf("workflow %s: image %s, entry %q, prebuilt %v; want the entry's own reference, team, true", name, ws.Image, ws.CatalogEntry, ws.CatalogPrebuilt)
		}
		if !ws.DeployJob {
			t.Errorf("workflow %s is not deployed: %v", name, missing)
		}
	}
}

// TestReadinessHoldsBackAPrebuiltEntryBeforeTheCatalogShips: the shipped gate
// covers a prebuilt entry exactly as it covers a built one, and its refusal
// names the entry rather than a build.
func TestReadinessHoldsBackAPrebuiltEntryBeforeTheCatalogShips(t *testing.T) {
	f, spec := prebuiltSpec(t)
	got, missing := catalogReadiness(t, f, spec, false)
	if !slices.ContainsFunc(missing, func(m Missing) bool {
		return m.Kind == MissingImage && strings.Contains(m.Reason, "entry team") && strings.Contains(m.Reason, "image_catalog")
	}) {
		t.Errorf("missing = %v, want the shipped gate's refusal naming the entry", missing)
	}
	for name, ws := range got.Workflows {
		if ws.DeployJob {
			t.Errorf("workflow %s is deployed on a catalog entry no released fugaro reads the key for", name)
		}
	}
}

// TestSpecRecordsABuiltCatalogEntry: a built entry's job carries the entry it
// runs and is not marked prebuilt, so readiness keeps asking the entry's own
// build record or tag about it.
func TestSpecRecordsABuiltCatalogEntry(t *testing.T) {
	_, spec := catalogSpec(t)
	for name, ws := range spec.Workflows {
		if ws.CatalogEntry != "base" || ws.CatalogPrebuilt {
			t.Errorf("workflow %s: entry %q, prebuilt %v; want base, false", name, ws.CatalogEntry, ws.CatalogPrebuilt)
		}
	}
}

// TestReadinessHoldsBackACatalogJobNoReleasedFugaroCanRun: the key ships with
// the catalog's own release (imageCatalogShipped), and until it does no job
// may be deployed on an entry — the runner image would meet the key as
// configuration it does not read. The refusal says so and names fugaro
// update, not a build the operator could run today.
func TestReadinessHoldsBackACatalogJobNoReleasedFugaroCanRun(t *testing.T) {
	f, spec := catalogSpec(t)
	key, err := gcp.CatalogRecordKey(spec.Slug, "base")
	if err != nil {
		t.Fatal(err)
	}
	f.gcs.Put(spec.Installation.RunsBucket, key, []byte(`{"entry":"base"}`))
	got, missing := catalogReadiness(t, f, spec, false)
	if !slices.ContainsFunc(missing, func(m Missing) bool {
		return m.Kind == MissingImage && strings.Contains(m.Reason, "fugaro update") && strings.Contains(m.Reason, "image_catalog")
	}) {
		t.Errorf("missing = %v, want the shipped gate's refusal naming fugaro update and image_catalog", missing)
	}
	for name, ws := range got.Workflows {
		if ws.DeployJob {
			t.Errorf("workflow %s is deployed on a catalog entry no released fugaro reads the key for", name)
		}
	}
}

// The shipped state itself is pinned here: every other readiness test sets the
// gate through catalogReadiness, so none of them would notice the gate being
// false in the code that ships (issue 1659: 0.17.0 was tagged that way).
func TestImageCatalogGateIsShipped(t *testing.T) {
	if !imageCatalogShipped {
		t.Fatal("imageCatalogShipped is false: readiness holds back every job a catalog entry gives its image to, in every release")
	}
}
