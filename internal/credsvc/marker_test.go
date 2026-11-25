package credsvc_test

// The workflow-secret marker (F18, G2, IS4): the label the INTAKE writes on a
// container `fugaro secrets shared set` made, and the one the SERVICE reads to
// tell a workflow secret's container from the project's own model key's.
//
// It lives in this external test file because the tie it holds crosses a
// package boundary the service's own code cannot cross: internal/intake
// consumes internal/credsvc (its handlers call the service's wire types), so
// credsvc cannot import intake to spell the two constants, and it spells them
// itself (workflowsecrets.go). A drift between the two spellings is silent in
// the worst direction — the service would refuse a container the intake had
// marked, taking a working installation's workflow secrets away — so the two
// are held together here by BEHAVIOUR rather than by a shared constant: the
// test reads intake's own two constants, puts them on a container, and asks the
// service to recognise it.
//
// Everything is hermetic: the client is the deployment's own generated Secret
// Manager client, pointed at a loopback server, so the request this test reads
// is the request the service would make (no account, no ADC, no cloud).
//
// The read is a LIST filtered by name, and that is not a convenience: the
// service's account holds `secretmanager.secrets.list` alone (the
// `fugaroSecretLister` role, F9), so a container's labels are readable only
// through a list of that container's own name — and the name filter is a
// PREFIX, so the answer can carry a longer container the test must not read as
// this one.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/intake"
	"google.golang.org/api/option"
	secretmanager "google.golang.org/api/secretmanager/v1"
)

// markerServer is a loopback Secret Manager that answers every list of secrets
// with the secrets the test gives it, and records the one request the service
// made so that the path and the filter are facts and not assumptions.
type markerServer struct {
	project string
	secrets []map[string]any
	// status is what the server answers with; 0 is 200.
	status int
	// path and filter are the last list request's, which is the one the
	// service's own client built.
	path, filter string
}

func (m *markerServer) start(t *testing.T) *secretmanager.Service {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.path, m.filter = r.URL.Path, r.URL.Query().Get("filter")
		w.Header().Set("Content-Type", "application/json")
		if m.status != 0 && m.status != http.StatusOK {
			w.WriteHeader(m.status)
			_, _ = w.Write([]byte(`{"error":{"code":500,"message":"the API's own sentence","status":"INTERNAL"}}`))
			return
		}
		body, err := json.Marshal(map[string]any{"secrets": m.secrets})
		if err != nil {
			t.Errorf("the fixture's answer could not be built: %v", err)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	svc, err := secretmanager.NewService(context.Background(),
		option.WithEndpoint(srv.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("the fixture's own Secret Manager client could not be built: %v", err)
	}
	return svc
}

// container is one secret in a list answer: its resource name and its labels.
func container(project, id string, labels map[string]string) map[string]any {
	return map[string]any{"name": "projects/" + project + "/secrets/" + id, "labels": labels}
}

// TestWorkflowSecretMarkerIsTheIntakesLabel is the tie: the label and the value
// the SERVICE recognises are the ones the INTAKE writes, read from intake's own
// constants and put on a container here. A drift in either direction — a
// renamed label, a changed value — leaves the service refusing a container the
// intake marked, which is the failure this test exists to make loud.
func TestWorkflowSecretMarkerIsTheIntakesLabel(t *testing.T) {
	if intake.LabelWorkflowSecret == "" || intake.WorkflowSecretMarker == "" {
		t.Fatalf("the intake's marker is empty (%q=%q), so nothing here could be recognised",
			intake.LabelWorkflowSecret, intake.WorkflowSecretMarker)
	}
	const project, id = "fugaro-creds-test", "fugaro_p_deploy-token"
	s := &markerServer{project: project, secrets: []map[string]any{
		container(project, id, map[string]string{intake.LabelWorkflowSecret: intake.WorkflowSecretMarker}),
	}}
	src := credsvc.SecretManagerPerSecrets{Project: project, Service: s.start(t)}

	marked, err := src.Marked(context.Background(), id)
	if err != nil {
		t.Fatalf("the marker read failed: %v", err)
	}
	if !marked {
		t.Errorf("the service did not recognise the intake's own label %q=%q: the two spellings have drifted",
			intake.LabelWorkflowSecret, intake.WorkflowSecretMarker)
	}
	// The request is the one the lister grant admits: a list of the container's
	// own name, at the project the service was configured with, and nothing
	// else of the container's metadata.
	if want := "/v1/projects/" + project + "/secrets"; s.path != want {
		t.Errorf("the service listed %q, want %q", s.path, want)
	}
	if want := "name:projects/" + project + "/secrets/" + id; s.filter != want {
		t.Errorf("the service listed with filter %q, want %q", s.filter, want)
	}
}

// TestWorkflowSecretMarkerRefusesEverythingElse is the other half: the marker
// is one label with one value, and every answer that is not exactly it is
// UNMARKED. The name filter is a prefix, so the case that matters most is the
// longer container: `fugaro_p_deploy-token-2` matches the filter for
// `fugaro_p_deploy-token`, and a service that read the first answer as its own
// would serve a workflow secret out of another container's labels.
func TestWorkflowSecretMarkerRefusesEverythingElse(t *testing.T) {
	const project, id = "fugaro-creds-test", "fugaro_p_deploy-token"
	for _, tc := range []struct {
		name    string
		secrets []map[string]any
	}{
		{"no container at all", nil},
		{"a container with no labels", []map[string]any{container(project, id, nil)}},
		{"another label", []map[string]any{container(project, id, map[string]string{"owner": "ops"})}},
		{"the marker's label with another value", []map[string]any{
			container(project, id, map[string]string{intake.LabelWorkflowSecret: "yes"}),
		}},
		{"a longer container's marker", []map[string]any{
			container(project, id+"-2", map[string]string{intake.LabelWorkflowSecret: intake.WorkflowSecretMarker}),
		}},
		{"the container beside a longer marked one", []map[string]any{
			container(project, id, nil),
			container(project, id+"-2", map[string]string{intake.LabelWorkflowSecret: intake.WorkflowSecretMarker}),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &markerServer{project: project, secrets: tc.secrets}
			src := credsvc.SecretManagerPerSecrets{Project: project, Service: s.start(t)}
			marked, err := src.Marked(context.Background(), id)
			if err != nil {
				t.Fatalf("the marker read failed: %v", err)
			}
			if marked {
				t.Errorf("the container was read as marked, and only %q=%q on THIS container's own name is the marker",
					intake.LabelWorkflowSecret, intake.WorkflowSecretMarker)
			}
		})
	}
}

// TestWorkflowSecretMarkerReadFailsClosed: a list that FAILED is an error and
// never a "no marker". The two have two fixes — one is the service's grant or
// the API's state and the other is the container's — and the callers refuse on
// the error, so a service that answered false here would serve a model
// credential out of a container it could not classify.
func TestWorkflowSecretMarkerReadFailsClosed(t *testing.T) {
	const project, id = "fugaro-creds-test", "fugaro_p_deploy-token"
	s := &markerServer{project: project, status: http.StatusInternalServerError}
	src := credsvc.SecretManagerPerSecrets{Project: project, Service: s.start(t)}
	marked, err := src.Marked(context.Background(), id)
	if err == nil {
		t.Fatalf("a failed list answered marked=%t and no error; it must be a read that failed", marked)
	}
	if !strings.Contains(err.Error(), "could not be listed") {
		t.Errorf("the failure is %q, want the container's labels could not be listed", err)
	}
	if marked {
		t.Errorf("a failed read answered marked=true")
	}
}

// TestWorkflowSecretMarkerNeedsAClientAndAContainer: a reader built without a
// client, a project or a name answers an ERROR and never an unmarked container,
// which is the same fail-closed rule read at the other end.
func TestWorkflowSecretMarkerNeedsAClientAndAContainer(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  credsvc.SecretManagerPerSecrets
		id   string
	}{
		{"no client", credsvc.SecretManagerPerSecrets{Project: "p"}, "fugaro_p_deploy-token"},
		{"no project", credsvc.SecretManagerPerSecrets{Service: &secretmanager.Service{}}, "fugaro_p_deploy-token"},
		{"no container", credsvc.SecretManagerPerSecrets{Project: "p", Service: &secretmanager.Service{}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			marked, err := tc.src.Marked(context.Background(), tc.id)
			if err == nil || marked {
				t.Errorf("a reader with %s answered marked=%t err=%v, want a failed read", tc.name, marked, err)
			}
		})
	}
}
