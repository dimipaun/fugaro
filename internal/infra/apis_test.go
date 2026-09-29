package infra

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"testing"

	"google.golang.org/api/googleapi"

	"github.com/dimipaun/fugaro/internal/gcpfake"
)

// errorInfo is a googleapi error carrying one ErrorInfo detail, as the
// client parses Google's answer, for consumer projects/1.
func errorInfo(code int, reason, service, message string) error {
	return errorInfoFor(code, reason, service, "projects/1", message)
}

func errorInfoFor(code int, reason, service, consumer, message string) error {
	md := map[string]any{"service": service}
	if consumer != "" {
		md["consumer"] = consumer
	}
	return &googleapi.Error{Code: code, Message: message, Details: []any{
		map[string]any{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "reason": reason, "domain": "googleapis.com", "metadata": md},
	}}
}

// Only the structured detail says an API is disabled: its ErrorInfo
// reason, for the API asked about, in the target project. The message
// alone doesn't.
func TestServiceDisabledReadsErrorInfo(t *testing.T) {
	const sched = "cloudscheduler.googleapis.com"
	p := target{project: "proj-1234", number: 1}
	for _, tc := range []struct {
		name string
		err  error
		tg   target
		want bool
	}{
		{"disabled", errorInfo(403, "SERVICE_DISABLED", sched, "Cloud Scheduler API has not been used"), p, true},
		{"wrapped", fmt.Errorf("reading: %w", errorInfo(403, "SERVICE_DISABLED", sched, "x")), p, true},
		{"consumer by project ID", errorInfoFor(403, "SERVICE_DISABLED", sched, "projects/proj-1234", "x"), p, true},
		{"another consumer", errorInfoFor(403, "SERVICE_DISABLED", sched, "projects/2", "x"), p, false},
		{"another consumer by ID", errorInfoFor(403, "SERVICE_DISABLED", sched, "projects/other", "x"), p, false},
		{"no consumer", errorInfoFor(403, "SERVICE_DISABLED", sched, "", "x"), p, false},
		{"number unknown, a project consumer", errorInfoFor(403, "SERVICE_DISABLED", sched, "projects/2", "x"), target{project: "proj-1234"}, true},
		{"number unknown, no consumer", errorInfoFor(403, "SERVICE_DISABLED", sched, "", "x"), target{project: "proj-1234"}, false},
		{"number unknown, not a project", errorInfoFor(403, "SERVICE_DISABLED", sched, "folders/2", "x"), target{project: "proj-1234"}, false},
		{"another API", errorInfo(403, "SERVICE_DISABLED", "logging.googleapis.com", "x"), p, false},
		{"permission denied", errorInfo(403, "IAM_PERMISSION_DENIED", sched, "Permission denied"), p, false},
		{"message only", &googleapi.Error{Code: 403, Message: "SERVICE_DISABLED: Cloud Scheduler API has not been used", Body: `SERVICE_DISABLED ` + sched}, p, false},
		{"legacy reason only", &googleapi.Error{Code: 403, Message: "disabled", Errors: []googleapi.ErrorItem{{Reason: "accessNotConfigured"}}}, p, false},
		{"not a 403", errorInfo(500, "SERVICE_DISABLED", sched, "x"), p, false},
		{"another domain", &googleapi.Error{Code: 403, Details: []any{map[string]any{"@type": "type.googleapis.com/google.rpc.ErrorInfo",
			"reason": "SERVICE_DISABLED", "domain": "example.com", "metadata": map[string]any{"service": sched, "consumer": "projects/1"}}}}, p, false},
		{"404", &googleapi.Error{Code: 404}, p, false},
		{"nil", nil, p, false},
	} {
		if got := serviceDisabled(tc.err, sched, tc.tg); got != tc.want {
			t.Errorf("%s: serviceDisabled = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A disabled Storage API is an environment error for the state bucket,
// the Terraform backend: never "no state bucket yet", which would offer a
// create (or say there is no installation).
func TestStateBucketStorageDisabled(t *testing.T) {
	f := newCloud(t)
	f.gcs.AddBucket(testStateBucket, testProjectNumber, tfstate)
	f.su.Disable("storage.googleapis.com", f.gcs.Server)
	exists, err := CheckStateBucket(context.Background(), f.c, "proj-1234", testStateBucket)
	var ue *UserError
	if err == nil || exists || errors.As(err, &ue) || !strings.Contains(err.Error(), "SERVICE_DISABLED") && !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("exists %v, err %v; want a remote error", exists, err)
	}
	for _, r := range f.gcs.Requests() {
		if r.Method != http.MethodGet {
			t.Errorf("sent %s %s", r.Method, r.Path)
		}
	}
}

// Discovery's lookups after the project's number compare the consumer
// with it: a SERVICE_DISABLED naming another project fails closed.
func TestDisabledAPIOtherConsumerFailsClosed(t *testing.T) {
	f := newCloud(t)
	spec := sandboxSpec(t)
	f.bootstrap(t, spec, newDisplay)
	f.m5(t, spec)
	f.su.Consumer = "projects/999"
	f.su.Disable("cloudscheduler.googleapis.com", f.sched.Server)
	im, _, err := DiscoverRepo(context.Background(), f.c, spec)
	var ue *UserError
	if err == nil || errors.As(err, &ue) || len(im.List) != 0 {
		t.Fatalf("err = %v, imports %v; want a remote error", err, im.List)
	}
}

// A disabled Resource Manager is a *ServiceDisabledError, which init
// offers to fix; any other failure is not.
func TestProjectNumberResourceManagerDisabled(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	f.su.Disable(ServiceResourceManager, f.crm.Server)
	_, err := ProjectNumber(ctx, f.c, "proj-1234")
	var sd *ServiceDisabledError
	if !errors.As(err, &sd) || sd.Service != ServiceResourceManager || sd.Project != "proj-1234" {
		t.Fatalf("err = %v, want a *ServiceDisabledError", err)
	}
	// Discovery and the state bucket check report it the same way.
	if _, err := DiscoverInstallation(ctx, f.c, installationSpec(t)); !errors.As(err, &sd) {
		t.Errorf("discovery: err = %v, want a *ServiceDisabledError", err)
	}
	if _, err := CheckStateBucket(ctx, f.c, "proj-1234", testStateBucket); !errors.As(err, &sd) {
		t.Errorf("state bucket: err = %v, want a *ServiceDisabledError", err)
	}

	f.crm.Refuse(http.StatusForbidden, "PERMISSION_DENIED", "IAM_PERMISSION_DENIED", "Permission denied")
	if _, err := ProjectNumber(ctx, f.c, "proj-1234"); err == nil || errors.As(err, &sd) {
		t.Fatalf("permission denied: err = %v, want another error", err)
	}
}

// EnableService enables the service and waits for its operation; an
// enabled service is a no-op, and a refused enable an error.
func TestEnableService(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	f.su.PendingPolls = 2
	f.su.Disable(ServiceResourceManager, f.crm.Server)
	restore := enablePoll
	enablePoll = 0
	t.Cleanup(func() { enablePoll = restore })
	if err := EnableService(ctx, f.c, "proj-1234", ServiceResourceManager); err != nil {
		t.Fatal(err)
	}
	if !f.su.Enabled(ServiceResourceManager) {
		t.Error("not enabled")
	}
	if n, err := ProjectNumber(ctx, f.c, "proj-1234"); err != nil || n != testProjectNumber {
		t.Fatalf("after the enable: %d, %v", n, err)
	}
	// Again: enabled already, still fine.
	if err := EnableService(ctx, f.c, "proj-1234", ServiceResourceManager); err != nil {
		t.Fatalf("enabling an enabled service: %v", err)
	}
	polls := 0
	for _, r := range f.su.Requests() {
		if r.Method == http.MethodGet && strings.HasPrefix(r.Path, "/v1/operations/") {
			polls++
		}
	}
	if polls != 3 {
		t.Errorf("%d operation polls, want 3 (two not done, then done; the second enable is a no-op, done at once)", polls)
	}

	f.su.Refuse(http.StatusForbidden, "PERMISSION_DENIED", "IAM_PERMISSION_DENIED", "Permission denied to enable service")
	if err := EnableService(ctx, f.c, "proj-1234", ServiceResourceManager); err == nil || !strings.Contains(err.Error(), "Permission denied") {
		t.Fatalf("refused enable: err = %v", err)
	}
}

// Without credentials and without a Service Usage endpoint there is no
// Service Usage client, so an enable fails instead of calling Google.
func TestEnableServiceNeedsEndpoint(t *testing.T) {
	f := newCloud(t)
	e := f.endpoints()
	e.ServiceUsage = ""
	c, err := NewClients(context.Background(), f.options(nil), e)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnableService(context.Background(), c, "proj-1234", ServiceResourceManager); err == nil || !strings.Contains(err.Error(), "service_usage") {
		t.Fatalf("err = %v", err)
	}
}

// disabledAddresses are the import addresses of the resources each API
// holds.
var disabledAddresses = map[string]string{
	"storage.googleapis.com":          "google_storage_bucket",
	"iam.googleapis.com":              "google_service_account|google_project_iam_custom_role",
	"artifactregistry.googleapis.com": "google_artifact_registry_repository",
	"run.googleapis.com":              "google_cloud_run_v2_job",
	"secretmanager.googleapis.com":    "google_secret_manager_secret",
	"logging.googleapis.com":          "google_logging",
	"cloudscheduler.googleapis.com":   "google_cloud_scheduler_job",
}

func heldBy(service, address string) bool {
	for _, typ := range strings.Split(disabledAddresses[service], "|") {
		if strings.Contains(address, "."+typ) {
			return true
		}
	}
	return false
}

// A lookup in an API that is disabled in the project finds nothing, since
// nothing can exist there: discovery plans a create for what that API
// holds, adopts the rest, and the readiness gates read the API's
// resources as missing.
func TestDisabledAPIsReadAsMissing(t *testing.T) {
	ctx := context.Background()
	spec := sandboxSpec(t)
	inst := installationSpec(t)
	seed := func(f *cloud) {
		f.bootstrap(t, spec, newDisplay)
		f.m5(t, spec)
		f.m5Installation(inst)
	}
	all := newCloud(t)
	seed(all)
	wantInst, err := DiscoverInstallation(ctx, all.c, inst)
	if err != nil {
		t.Fatal(err)
	}
	wantRepo, _, err := DiscoverRepo(ctx, all.c, spec)
	if err != nil {
		t.Fatal(err)
	}
	for service := range disabledAddresses {
		t.Run(service, func(t *testing.T) {
			f := newCloud(t)
			seed(f)
			servers := map[string]*gcpfake.Server{
				"storage.googleapis.com": f.gcs.Server, "iam.googleapis.com": f.iam.Server, "artifactregistry.googleapis.com": f.ar.Server,
				"run.googleapis.com": f.run.Server, "secretmanager.googleapis.com": f.sm.Server, "logging.googleapis.com": f.logs.Server,
				"cloudscheduler.googleapis.com": f.sched.Server,
			}
			f.su.Disable(service, servers[service])
			check := func(what string, got, full Imports) {
				t.Helper()
				want := map[string]string{}
				for k, v := range importMap(full) {
					if !heldBy(service, k) {
						want[k] = v
					}
				}
				if g := importMap(got); !maps.Equal(g, want) {
					t.Errorf("%s imports:\n got %v\nwant %v", what, g, want)
				}
			}
			im, err := DiscoverInstallation(ctx, f.c, inst)
			if err != nil {
				t.Fatalf("installation: %v", err)
			}
			check("installation", im, wantInst)
			rim, ex, err := DiscoverRepo(ctx, f.c, spec)
			if err != nil {
				t.Fatalf("repo: %v", err)
			}
			check("repo", rim, wantRepo)
			if _, _, err := Readiness(ctx, f.c, spec, ex); err != nil {
				t.Fatalf("readiness: %v", err)
			}
			if len(servers[service].Requests()) == 0 {
				t.Error("nothing asked the disabled API: the case tests nothing")
			}
		})
	}
}

// The readiness gates read a disabled API's resources as missing: no
// secret version, no image, no build record.
func TestReadinessDisabledAPIsMissing(t *testing.T) {
	ctx := context.Background()
	spec := sandboxSpec(t)
	for _, service := range []string{"secretmanager.googleapis.com", "artifactregistry.googleapis.com"} {
		t.Run(service, func(t *testing.T) {
			f := newCloud(t)
			f.bootstrap(t, spec, newDisplay)
			f.m5(t, spec)
			_, ex, err := DiscoverRepo(ctx, f.c, spec)
			if err != nil {
				t.Fatal(err)
			}
			s := map[string]*gcpfake.Server{"secretmanager.googleapis.com": f.sm.Server, "artifactregistry.googleapis.com": f.ar.Server}[service]
			f.su.Disable(service, s)
			_, missing, err := Readiness(ctx, f.c, spec, ex)
			if err != nil {
				t.Fatal(err)
			}
			want := MissingSecret
			if service == "artifactregistry.googleapis.com" {
				want = MissingImage
			}
			found := false
			for _, m := range missing {
				found = found || m.Kind == want
			}
			if !found {
				t.Errorf("missing = %v, want a %s gate", missing, want)
			}
		})
	}
}

// A 403 that isn't SERVICE_DISABLED (a missing permission) still fails
// closed, as a remote error with no imports.
func TestPermissionDeniedStillFailsClosed(t *testing.T) {
	ctx := context.Background()
	spec := sandboxSpec(t)
	inst := installationSpec(t)
	for _, tc := range []struct {
		name string
		srv  func(*cloud) *gcpfake.Server
		repo bool
	}{
		{"scheduler", func(f *cloud) *gcpfake.Server { return f.sched.Server }, true},
		{"logging", func(f *cloud) *gcpfake.Server { return f.logs.Server }, false},
		{"iam", func(f *cloud) *gcpfake.Server { return f.iam.Server }, false},
		{"run", func(f *cloud) *gcpfake.Server { return f.run.Server }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCloud(t)
			f.bootstrap(t, spec, newDisplay)
			f.m5(t, spec)
			f.m5Installation(inst)
			tc.srv(f).Refuse(http.StatusForbidden, "PERMISSION_DENIED", "IAM_PERMISSION_DENIED", "Permission denied")
			var im Imports
			var err error
			if tc.repo {
				im, _, err = DiscoverRepo(ctx, f.c, spec)
			} else {
				im, err = DiscoverInstallation(ctx, f.c, inst)
			}
			var ue *UserError
			if err == nil || errors.As(err, &ue) || len(im.List) != 0 {
				t.Errorf("err = %v, imports %v; want a remote error and no imports", err, im.List)
			}
		})
	}
}
