// Package credsvcfake stands the installation's credentials service up over the
// hermetic fakes, for the tests outside internal/credsvc that need the REAL
// route rather than a stand-in for it.
//
// Everything the service reads on a workflow-secrets call is here as the
// installation really holds it: the records in a Realtime Database
// (internal/gcpfake's), the containers in Secret Manager (gcpfake's), read
// through the service's own readers — creds.ReadProjectSecrets and
// creds.ReadWorkflowSecrets behind credsvc's RTDB readers, and
// SecretManagerPerSecrets over a real secretmanager client pointed at the fake,
// which is what reads a value and a container's marker alike. What is NOT here
// is Google's own identity check: the caller class is a stand-in that answers
// the identity this fixture's job holds (credsvc's Authenticator is the
// deployment's, and internal/credsvc's own tests cover it).
//
// The three writes a test makes through the installation are the three the
// shipped commands make, each named after its own: Allow is `fugaro secrets
// allow`'s record (IS3), SetShared is what `fugaro secrets shared set` and the
// migration's copy leave in the grammar's container (IS5), and
// RecordWorkflowSecret is `fugaro update`'s per-repository record (B3.1). Every
// one of them goes through the same writer the command uses, so a test that
// wires a world through this package is a test about what the service serves an
// installation in that state.
package credsvcfake

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"google.golang.org/api/option"
	secretmanager "google.golang.org/api/secretmanager/v1"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/intake"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/secretname"
	"github.com/dimipaun/fugaro/internal/task"
)

// Options is the installation a test asks for. Every field has a default, so a
// test that needs nothing special passes the zero value.
type Options struct {
	// Provider is the repository's provider (gitprov.KindGitHub by default).
	Provider string
	// Repo is the repository the fixture's job account is bound to, in the
	// spelling the owner's files use (owner/name).
	Repo string
	// Workflow is the workflow that job account names, which is the key its
	// version-1 record is read under.
	Workflow string
	// Project is the GCP project the installation lives in.
	Project string
	// JobAccount is the verified service account the service pairs a call with.
	JobAccount string
}

// The fixture's defaults. The account is shaped like a deployment's and is not
// one, like every other name in these tests.
const (
	defaultProvider   = gitprov.KindGitHub
	defaultRepo       = "acme/aurora"
	defaultWorkflow   = "app"
	defaultProject    = "aurora"
	defaultJobAccount = "fugaro-job@aurora-dev.iam.gserviceaccount.com"
)

// Installation is the service and the fakes behind it.
type Installation struct {
	// Handler is the service itself: what a test puts behind a front door of
	// its own, or drives directly.
	Handler http.Handler
	// URL is the service on a loopback listener, which is what a job's
	// environment carries (runner.CredentialsServiceURLEnv).
	URL string
	// DB is the installation's database: the records a call is admitted by and
	// served from, read through the real codecs.
	DB *rtdb.Client
	// Secrets is the fake Secret Manager the containers live in, for a test
	// that asserts what a call left there or seeds a container of its own.
	Secrets *gcpfake.Secrets
	// Repo is the fixture's repository as the local block spells it, Canonical
	// its provider-qualified form (what the job account's row carries) and Slug
	// the slug of a run of it.
	Repo, Canonical, Slug string
	// Workflow is the workflow the fixture's job account names.
	Workflow string
	// Project is the GCP project.
	Project string
	// JobAccount is the verified account the service binds the call to.
	JobAccount string

	audit *auditLog
}

// New starts the service over fresh fakes, which live until the test ends.
func New(t *testing.T, o Options) *Installation {
	t.Helper()
	if o.Provider == "" {
		o.Provider = defaultProvider
	}
	if o.Repo == "" {
		o.Repo = defaultRepo
	}
	if o.Workflow == "" {
		o.Workflow = defaultWorkflow
	}
	if o.Project == "" {
		o.Project = defaultProject
	}
	if o.JobAccount == "" {
		o.JobAccount = defaultJobAccount
	}
	canonical, err := projectpolicy.Canonical(o.Provider, o.Repo)
	if err != nil {
		t.Fatalf("the fixture's repository %q is not one: %v", o.Repo, err)
	}
	slug, err := task.Slug(o.Provider, o.Repo)
	if err != nil {
		t.Fatalf("the fixture's repository %q has no slug: %v", o.Repo, err)
	}

	db := gcpfake.NewRTDB(t)
	client, err := rtdb.New(db.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatalf("the fake database's client: %v", err)
	}
	secrets := gcpfake.NewSecrets(t)
	// A test's client, so the service reads a value and a container's marker
	// through the same calls a deployment makes; the fake refuses the access
	// call unless the test allows it, which is the point of the fake.
	secrets.AllowAccess()
	sm, err := secretmanager.NewService(context.Background(),
		option.WithEndpoint(secrets.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("the fake Secret Manager's client: %v", err)
	}

	i := &Installation{
		DB: client, Secrets: secrets, Repo: o.Repo, Canonical: canonical, Slug: slug,
		Workflow: o.Workflow, Project: o.Project, JobAccount: o.JobAccount,
		audit: &auditLog{},
	}
	ident := credsvc.Identity{
		Slug:       slug,
		Run:        "run-fixture",
		Project:    o.Project,
		Job:        gcp.JobName(slug, gcp.WorkflowVariant(o.Workflow, "")),
		JobAccount: o.JobAccount,
		// The class this route serves: a workflow secret is served only to the
		// per-repository job of the workflow it is recorded for (G19).
		JobClass: credsvc.ClassRepository,
	}
	srv, err := credsvc.NewServer(credsvc.Config{
		Auth: verifier{ident: ident},
		// The bundle's own source, which is also what the per-secret reads and
		// the marker LIST are built from: the deployment's one client over the
		// installation's one project.
		Bundles: &credsvc.BundleLoader{Source: credsvc.SecretManagerSource{
			Project: o.Project, Secret: gcp.CredsBundleSecretID(o.Project), Service: sm,
		}},
		Policy:   noPolicy{},
		State:    &creds.RTDBStore{DB: client},
		Kills:    noKills{},
		DB:       client,
		Probes:   client,
		Slips:    &creds.RTDBStore{DB: client},
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		AuditLog: slog.NewJSONHandler(i.audit, nil),
		JobAccounts: []credsvc.JobAccount{
			{Email: o.JobAccount, Slug: slug, Repo: canonical, Workflow: o.Workflow},
		},
		WorkflowSecrets: credsvc.RTDBWorkflowSecrets{DB: client},
		ProjectSecrets:  credsvc.RTDBProjectSecrets{DB: client},
	})
	if err != nil {
		t.Fatalf("building the credentials service: %v", err)
	}
	i.Handler = srv
	listener := httptest.NewServer(srv)
	t.Cleanup(listener.Close)
	i.URL = listener.URL
	return i
}

// Allow is the record `fugaro secrets allow NAME owner/repo` writes (IS3): the
// named repositories may fetch that name. No repository is the fixture's own.
func (i *Installation) Allow(t *testing.T, name string, repos ...string) {
	t.Helper()
	if len(repos) == 0 {
		repos = []string{i.Repo}
	}
	ctx := context.Background()
	err := creds.WriteProjectSecrets(ctx, i.DB, func(cur creds.ProjectSecrets, _ bool) (creds.ProjectSecrets, error) {
		if cur.Secrets == nil {
			cur.Secrets = map[string]creds.ProjectSecretRule{}
		}
		cur.Secrets[name] = creds.ProjectSecretRule{Repos: repos}
		return cur, nil
	})
	if err != nil {
		t.Fatalf("writing the project record for %s: %v", name, err)
	}
}

// SetShared is the value in the grammar's own container, marked as a workflow
// secret's: what `fugaro secrets shared set NAME` leaves, and what the
// migration's copy leaves when it moves a repository's secret into the project
// (IS5, B3.6). The name is the logical one and the container is
// secretname.Project's, exactly as the route builds it.
func (i *Installation) SetShared(t *testing.T, name, value string) {
	t.Helper()
	id, err := secretname.Project(name)
	if err != nil {
		t.Fatalf("the grammar refuses the name %q: %v", name, err)
	}
	i.Secrets.Seed(id, map[string]string{
		intake.LabelScope:          string(intake.ScopeShared),
		intake.LabelWorkflowSecret: intake.WorkflowSecretMarker,
	}, []byte(value))
}

// RecordWorkflowSecret is B3.1's per-repository record for the fixture's
// workflow: the variable the workflow declares is read from container, and
// container holds value. It is what `fugaro update` writes for a repository
// whose secrets its job still mounts.
func (i *Installation) RecordWorkflowSecret(t *testing.T, env, container, value string) {
	t.Helper()
	rec := creds.WorkflowSecrets{
		Workflows: map[string][]creds.WorkflowSecret{
			i.Workflow: {{Env: env, ID: container}},
		},
	}
	if err := creds.PutWorkflowSecrets(context.Background(), i.DB, i.Slug, rec); err != nil {
		t.Fatalf("writing the workflow secrets record for %s: %v", i.Slug, err)
	}
	i.Secrets.Seed(container, nil, []byte(value))
}

// AuditLines is every audit line the service wrote, parsed as the JSON objects
// it writes them as (C1.13), so that a test can assert the service recorded an
// act rather than that it answered.
func (i *Installation) AuditLines(t *testing.T) []map[string]any {
	t.Helper()
	raw := strings.TrimSpace(i.audit.text())
	if raw == "" {
		return nil
	}
	out := make([]map[string]any, 0, 1)
	for _, line := range strings.Split(raw, "\n") {
		var fields map[string]any
		if err := json.Unmarshal([]byte(line), &fields); err != nil {
			t.Fatalf("the service's audit line %q is not the JSON object it writes: %v", line, err)
		}
		out = append(out, fields)
	}
	return out
}

// verifier is the caller class this fixture's service verifies. A deployment
// builds it out of Google's own key set (credsvc's Authenticator); what the
// workflow route needs of it is the one answer Verify gives, and every other
// caller class refuses, so a route this fixture does not serve cannot be
// reached by accident.
type verifier struct{ ident credsvc.Identity }

func (v verifier) Verify(context.Context, credsvc.Request) (credsvc.Identity, error) {
	return v.ident, nil
}

func (v verifier) refuse() error {
	return errors.New("credsvcfake: this fixture serves the workflow-secrets route alone")
}

func (v verifier) VerifyVerifier(context.Context, string) (string, error) { return "", v.refuse() }
func (v verifier) VerifyReadCaller(context.Context, string) (string, error) {
	return "", v.refuse()
}
func (v verifier) VerifyPersonCaller(context.Context, string) (string, error) {
	return "", v.refuse()
}
func (v verifier) VerifyOrchestrator(context.Context, string) (string, error) {
	return "", v.refuse()
}
func (v verifier) VerifyLauncher(context.Context, string) (credsvc.LauncherCaller, error) {
	return credsvc.LauncherCaller{}, v.refuse()
}
func (v verifier) VerifyBuildCaller(context.Context, string) (string, error) {
	return "", v.refuse()
}
func (v verifier) VerifyRepoBuild(context.Context, string) (credsvc.RepoBuildAccount, error) {
	return credsvc.RepoBuildAccount{}, v.refuse()
}

// noPolicy is a published policy the workflow route never reads: the route's
// admission is the record and the container, and the published node is not part
// of it. An installation with no policy published is the state every one of
// them starts in, so the absent answer is the honest one.
type noPolicy struct{}

func (noPolicy) Shared(context.Context) (*projectpolicy.Policy, bool, error) { return nil, false, nil }
func (noPolicy) Admissions(context.Context, string) (*projectpolicy.RepoPolicy, bool, error) {
	return nil, false, nil
}

// noKills is an installation whose stop switches are all off.
type noKills struct{}

func (noKills) Stop(context.Context, string) (bool, string, error) { return false, "", nil }

// auditLog is a buffer the test reads while the service's own goroutine writes
// it: the act is audited before the answer is written and the client reads the
// answer, so the read follows the write in time — and the lock is what makes
// that a fact rather than a hope.
type auditLog struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *auditLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *auditLog) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
