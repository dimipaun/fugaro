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
