package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// TestServicesStampAlwaysWritten: the launch stamps the services the job's
// catalog entry declares into the run's environment, and it does so on EVERY
// launch — the empty value included (D1904).
//
// The always is the whole of the rule (backend.ServicesEnv): the runner reads
// its own environment, and on a job whose image a catalog entry chose, that
// environment carries the image's own ENV. An image that set FUGARO_SERVICES
// would then be answering for the entry — a list read from somewhere other
// than the entry, which is what TestServicesFromEntryOnly forbids — so the
// launch writes the key whatever the answer is, and the empty value overrides
// what the image holds with "no entry declared a service".
//
// The key is therefore the LAUNCHER's to write, and the second half pins the
// other side of that: a launch entry naming it is not refused by the backends
// (it is not one of backend.ReservedEnvNames), which is what lets the value
// above travel at all — while the deploy's own stamp, which the launcher must
// not be able to rewrite, is (ImageEntryEnv).
func TestServicesStampAlwaysWritten(t *testing.T) {
	for _, tc := range []struct {
		name  string
		entry config.Entry
		want  string
	}{
		{"one service", config.Entry{Services: []string{"postgres"}}, backend.ServicesEnv + "=postgres"},
		{"three services", config.Entry{Services: []string{"postgres", "redis", "firebase"}}, backend.ServicesEnv + "=postgres redis firebase"},
		{"no service declared", config.Entry{}, backend.ServicesEnv + "="},
		{"an entry the catalog does not speak for", config.Entry{Name: "web", Tags: []string{"node"}}, backend.ServicesEnv + "="},
	} {
		got := servicesLaunchEnv(tc.entry)
		if got != tc.want {
			t.Errorf("%s: servicesLaunchEnv = %q, want %q", tc.name, got, tc.want)
		}
		if !strings.HasPrefix(got, backend.ServicesEnv+"=") {
			t.Errorf("%s: servicesLaunchEnv = %q: the key is written on every launch", tc.name, got)
		}
	}

	// The launcher's entry reaches the job, empty value and all: LaunchEnv is
	// the seam both backends build an execution's environment through, so a
	// name it refuses is one no backend can carry, and the key above has to
	// travel for the runner to hear the entry at all.
	env, err := backend.LaunchEnv(backend.LaunchSpec{Env: []string{
		servicesLaunchEnv(config.Entry{}),
	}}, backend.ReservedEnvNames("FUGARO_RUN")...)
	if err != nil {
		t.Fatalf("a launch entry naming %s was refused: %v — the launcher is the writer of that key", backend.ServicesEnv, err)
	}
	if len(env) != 1 || env[0].Name != backend.ServicesEnv || env[0].Value != "" {
		t.Errorf("the carried entry = %+v, want %s with the empty value", env, backend.ServicesEnv)
	}
	// The contrast, one line further: the deploy's own stamp is the one a
	// launch may not rewrite, and the backends refuse an entry naming it.
	if _, err := backend.LaunchEnv(backend.LaunchSpec{Env: []string{backend.ImageEntryEnv + "=web"}},
		backend.ReservedEnvNames(backend.ImageEntryEnv)...); err == nil {
		t.Errorf("a launch entry naming %s was accepted, and the deploy is its writer", backend.ImageEntryEnv)
	}
}

// servicesEntryImage is the image the fixture's catalog gives the web job: the
// entry the launch resolves from the DEPLOYED job (D1149).
const servicesEntryImage = "ghcr.io/acme/app:1"

// servicesCatalogYAML is a project catalog whose entry provides two services.
const servicesCatalogYAML = `image_catalog:
  entries:
    - name: app
      tags: [node]
      services: [postgres, redis]
      source: { kind: image, image: ghcr.io/acme/app:1 }
`

// TestServicesStampReachesTheLaunchFromTheLocalCatalog and
// TestServicesStampReachesTheLaunchFromThePublishedNode are the two halves of
// the resolution the stamp is written from (D1904), and they are the halves a
// machine can differ in: the launcher reads the entry the job was DEPLOYED
// with, and it takes that entry's services from the side the entry itself came
// from — this machine's own catalog when it writes one (which is a checkout
// that has run `fugaro project image add`), and the published node's
// `image_services` key when the node is what speaks for the catalog, which is
// the teammate's machine and CI: a clone with no catalog of its own at all
// (localcfg's keep rule, ImageCatalogEffective).
//
// The node's half is the one that fails silently if it breaks, and that is why
// it is pinned here rather than by the publisher's tests: an install that
// dropped the read starts NO service, and nothing says so — every test of an
// image that declares one fails in a stage's output, on a machine whose
// operator has no catalog to look at. The node below carries a second entry
// with a different list, so a launcher that answered with the map, with another
// entry's list or with the script's own default fails here too.
//
// Each half is run in both checkout states (forEachCheckoutState): the entry is
// read from the DEPLOYED job's image, and the workflow file only decides
// whether the catalog speaks for that workflow — so a launcher with no checkout
// of the repository, which is every console launch, still stamps the entry it
// is about to start. That is a fixed bug of this branch's own: the checkout was
// read first, and a launch without one stamped the empty list.
//
// Mutations (verified, run and restored): drop
// `entry.Services = pol.ServicesFor(...)` in deployedCatalogEntry (the node
// half only: the local catalog still carries its own list), and the node cases
// launch with an empty stamp; return early from deployedCatalogEntry when
// checkoutConfig has nothing (`co == nil`), as the branch did, and both
// outside-any-checkout cases launch with an empty stamp while both checkout
// cases pass.

// servicesLaunchJob is the deployment read a launch makes of its backend: the
// real one (gcp.Backend.JobImage) answers with the image the job was deployed
// with, and the image is what the entry is resolved from.
type servicesLaunchJob struct {
	backend.Backend
	job   string
	image string
}

func (b servicesLaunchJob) JobImage(_ context.Context, job string) (string, bool, error) {
	if job != b.job {
		return "", false, fmt.Errorf("the launcher read job %s, not the workflow's own %s", job, b.job)
	}
	return b.image, true, nil
}

// servicesLaunchBackend keeps the environment the launch handed the execution,
// as envRecorder does: what is asserted here is that list, so the test needs
// the recorder to be the backend the entry is resolved from as well (JobImage).
type servicesLaunchBackend struct {
	servicesLaunchJob
	env []string
}

func (b *servicesLaunchBackend) Launch(ctx context.Context, spec backend.LaunchSpec) (backend.ExecutionRef, error) {
	b.env = spec.Env
	return b.servicesLaunchJob.Backend.Launch(ctx, spec)
}

// servicesLaunch launches the fixture's run with the recorder in place of the
// backend, and returns it.
func servicesLaunch(t *testing.T, f *cloudFixture, image string) *servicesLaunchBackend {
	t.Helper()
	env := memEnv(t, f)
	rec := &servicesLaunchBackend{servicesLaunchJob: servicesLaunchJob{
		Backend: env.be, job: gcp.JobName(appSlug, "web"), image: image}}
	env.be = rec
	spec := raceSpec(t, env)
	if _, err := launchRun(context.Background(), env, appSlug, spec, time.Now()); err != nil {
		t.Fatal(err)
	}
	return rec
}

// servicesStamp is the value the launch wrote under backend.ServicesEnv, and it
// fails unless the launch wrote exactly one: the key is written on every launch
// (servicesLaunchEnv), so a launch that wrote none, or two, is a failure of the
// always and not a value to compare.
func servicesStamp(t *testing.T, plain []string) string {
	t.Helper()
	value, n := "", 0
	for _, kv := range plain {
		if v, ok := strings.CutPrefix(kv, backend.ServicesEnv+"="); ok {
			value, n = v, n+1
		}
	}
	if n != 1 {
		t.Fatalf("the launch's plain Env carries %s %d times (%v): every launch writes it once", backend.ServicesEnv, n, plain)
	}
	return value
}

// forEachCheckoutState runs body in the two states a launcher can be in about
// the repository's checkout: with one — the operator's own, where fugaro.yaml
// is readable and the resolver can be asked — and without one, which is
// every launch the desktop console spawns (`fugaro run --repo` from a private
// empty directory) and every `fugaro run --repo` from anywhere else. The stamp
// has to be the deployed entry's in BOTH, and the without is the half that
// fails silently: with no file to read, the entry is the deployed job's image
// alone, and a launcher that gave up on it there starts the services of no
// entry at all — every test of an image that declares one then fails in a
// stage's output, with no word about the services being the reason (F12).
func forEachCheckoutState(t *testing.T, body func(t *testing.T)) {
	t.Helper()
	for _, tc := range []struct {
		name     string
		checkout bool
	}{
		{"from the repository's checkout", true},
		{"outside any checkout", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.checkout {
				dockerCheckout(t, needsNode)
			} else {
				testutil.IsolateGit(t)
				t.Chdir(t.TempDir())
			}
			body(t)
		})
	}
}

func TestServicesStampReachesTheLaunchFromTheLocalCatalog(t *testing.T) {
	forEachCheckoutState(t, func(t *testing.T) {
		f := newLaunchBudget(t, "enforce")
		f.appendConfig(t, servicesCatalogYAML)
		rec := servicesLaunch(t, f.cloudFixture, servicesEntryImage)
		if got, want := servicesStamp(t, rec.env), "postgres redis"; got != want {
			t.Errorf("the launch's %s = %q, want %q: the entry the job was deployed with declares them, and this machine's own catalog is what speaks for that entry here", backend.ServicesEnv, got, want)
		}
	})
}

// servicesNode is the published policy node of the second case: the same
// catalog, the services as the top-level key the node carries them under (the
// entry's own block decodes strictly and may not gain a member, F20), and a
// second entry whose list is different so the stamp cannot be any service the
// node mentions.
func servicesNode() map[string]any {
	return map[string]any{
		"version": projectpolicy.Version,
		"image_catalog": map[string]any{"entries": []any{
			map[string]any{"name": "app", "tags": []any{"node"},
				"source": map[string]any{"kind": "image", "image": servicesEntryImage}},
			map[string]any{"name": "tools", "tags": []any{"node"},
				"source": map[string]any{"kind": "image", "image": "ghcr.io/acme/tools:1"}},
		}},
		"image_services": map[string]any{"app": []any{"postgres", "redis"}, "tools": []any{"firebase"}},
	}
}

func TestServicesStampReachesTheLaunchFromThePublishedNode(t *testing.T) {
	forEachCheckoutState(t, func(t *testing.T) {
		f := newLaunchBudget(t, "enforce")
		f.db.Set(projectpolicy.PathRulesMarker(), projectpolicy.PolicyRulesVersion)
		f.db.Set(projectpolicy.PathShared(), servicesNode())
		if cfg := readFileString(t, os.Getenv("FUGARO_CONFIG")); strings.Contains(cfg, "image_catalog") {
			t.Fatalf("the fixture's machine writes a catalog of its own, so this case is not the one it is about:\n%s", cfg)
		}
		rec := servicesLaunch(t, f.cloudFixture, servicesEntryImage)
		if got, want := servicesStamp(t, rec.env), "postgres redis"; got != want {
			t.Errorf("the launch's %s = %q, want %q: the node's image_services carries this entry's list, and the node is the only side that speaks for the catalog on a machine with no catalog of its own", backend.ServicesEnv, got, want)
		}
	})
}
