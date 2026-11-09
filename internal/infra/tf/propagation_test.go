package tf

import (
	"encoding/json"
	"errors"
	"testing"
)

// The closed list of propagation errors (task: "fugaro update absorbs a Google
// API that has not caught up with a resource it just created"). Every case here
// is one row of it: what Google's API answers while it has not yet seen a
// resource the same apply created, and — just as much — what it looks like when
// the error is something else.
//
// The fixtures are the shapes terraform really writes, so a test asserts about
// the text an apply leaves behind rather than about a one-line error nobody
// sees. Both of terraform's shapes are here: the boxed one it draws on a
// terminal, and the piped one it writes when stderr is not a terminal --
// which is the one every fugaro apply produces, because fugaro pipes stderr
// (tf.go's MultiWriter).

// applyErr is the error one terraform apply left behind, as the wrapper builds
// it: the tail of stderr and the exit code.
func applyErr(stderr string) error {
	return &ExitError{Command: "apply", Code: 1, Stderr: stderr}
}

// withResource renders one terraform apply diagnostic the way terraform draws
// it on a terminal: boxed, a blank line, and the resource it acts on above the
// source line.
func withResource(address, message string) string {
	return "╷\n│ Error: " + message + "\n│ \n│   with " + address + ",\n│   on iam.tf line 163, in resource \"x\" \"y\":\n│  163: resource \"x\" \"y\" {\n│ \n╵\n"
}

// withResourcePiped renders the same diagnostic the way terraform writes it
// when its output is not a terminal -- which is every apply fugaro runs, since
// it pipes stderr: no box, no rule lines, the message (wrapped by terraform
// where a caller passes it wrapped) and the "  with <address>," line below it.
// Both are terraform's own shapes and the classifier reads both: the box is
// what a person sees when they run terraform themselves, the piped one is what
// an apply writes into the tail fugaro keeps.
func withResourcePiped(address, message string) string {
	return "Error: " + message + "\n  with " + address + ",\n  on iam.tf line 163, in resource \"x\" \"y\":\n  163: resource \"x\" \"y\" {\n"
}

// theReported404 is the failure of 2026-11-09, 0.21.1 on a managed project:
// `terraform apply` created google_cloud_run_v2_service.credentials[0] and the
// two grants on it failed against an IAM front end that had not seen it.
var (
	invoker404  = "Error applying IAM policy for cloudrun.googleapis.com/Service \"fugaro-credentials\": googleapi: Error 404: Resource 'fugaro-credentials' of kind 'SERVICE' does not exist, notFound"
	verifier404 = "Error applying IAM policy for cloudrun.googleapis.com/Service \"fugaro-credentials\": googleapi: Error 404: Resource 'fugaro-credentials' of kind 'SERVICE' does not exist, notFound"
	// theGap400 is what the compute root's own gap reads (design
	// compute-backend §7.2 finding 5): a binding naming a service account IAM
	// has not seen yet.
	theGap400 = "Error applying IAM policy for project \"proj-1234\": Error setting IAM policy for project \"proj-1234\": googleapi: Error 400: Service account fugaro-reaper@proj-1234.iam.gserviceaccount.com does not exist., badRequest"
)

// planOf builds a plan of creates only, each change's own values as terraform
// would have written them.
func planOf(changes ...ResourceChange) *Plan {
	return &Plan{FormatVersion: "1.2", ResourceChanges: changes}
}

// create is one planned create with the attributes a change carries after it.
func create(addr, typ string, after map[string]any) ResourceChange {
	return ResourceChange{Address: addr, Mode: "managed", Type: typ,
		Change: Change{Actions: []string{"create"}, Before: map[string]any{}, After: after}}
}

// credentialsServicePlan is the installation root's plan in the reported
// failure: the service this apply creates, and the two grants on it.
func credentialsServicePlan() *Plan {
	return planOf(
		create("module.installation.google_cloud_run_v2_service.credentials[0]", "google_cloud_run_v2_service",
			map[string]any{"name": "fugaro-credentials", "location": "us-east5", "project": "proj-1234"}),
		create("module.installation.google_cloud_run_v2_service_iam_member.credentials_invoker", "google_cloud_run_v2_service_iam_member",
			map[string]any{"name": "fugaro-credentials", "project": "proj-1234", "location": "us-east5", "role": "roles/run.invoker", "member": "user:operator@example.invalid"}),
		create("module.installation.google_cloud_run_v2_service_iam_member.credentials_verifier", "google_cloud_run_v2_service_iam_member",
			map[string]any{"name": "fugaro-credentials", "project": "proj-1234", "location": "us-east5", "role": "roles/run.invoker", "member": "serviceAccount:fugaro-verifier@proj-1234.iam.gserviceaccount.com"}),
	)
}

// TestPropagationFailureIsTheClosedList is the list itself.
func TestPropagationFailureIsTheClosedList(t *testing.T) {
	tests := []struct {
		name string
		err  error
		plan *Plan

		want bool
	}{
		{
			// The reported failure, whole: two 404s, both on a grant whose
			// target this very apply created.
			name: "the two 404s of the reported failure",
			err:  applyErr(withResource("module.installation.google_cloud_run_v2_service_iam_member.credentials_invoker", invoker404) + withResource("module.installation.google_cloud_run_v2_service_iam_member.credentials_verifier", verifier404)),
			plan: credentialsServicePlan(),
			want: true,
		},
		{
			// The same failure as the apply really writes it, since fugaro
			// pipes stderr: no box, and the message broken across lines the
			// way terraform breaks it.
			name: "the piped shape of the reported failure",
			err: applyErr(withResourcePiped("module.installation.google_cloud_run_v2_service_iam_member.credentials_invoker",
				"Error applying IAM policy for cloudrun.googleapis.com/Service \"fugaro-credentials\": googleapi: Error 404: Resource 'fugaro-credentials' of kind 'SERVICE'\ndoes not exist, notFound")),
			plan: credentialsServicePlan(),
			want: true,
		},
		{
			// And the piped shape of a failure that must stop at once: the
			// shape is not what decides, the message is.
			name: "the piped shape of one 404 mixed with another failure",
			err: applyErr(withResourcePiped("module.installation.google_cloud_run_v2_service_iam_member.credentials_invoker",
				"Error applying IAM policy for cloudrun.googleapis.com/Service \"fugaro-credentials\": googleapi: Error 404: Resource 'fugaro-credentials' of kind 'SERVICE' does not exist, notFound") +
				withResourcePiped("module.installation.google_cloud_run_v2_job.history", "Error creating job: googleapi: Error 403: Quota exceeded, quotaExceeded")),
			plan: credentialsServicePlan(),
		},
		{
			// A 400 on a grant to the account that same apply created: the
			// compute root's own gap, and now the same list.
			name: "a 400 on a grant to a service account this apply created",
			err:  applyErr(withResource("module.compute.google_project_iam_member.reaper", theGap400)),
			plan: planOf(
				create("module.compute.google_service_account.reaper", "google_service_account",
					map[string]any{"account_id": "fugaro-reaper", "email": "fugaro-reaper@proj-1234.iam.gserviceaccount.com"}),
				create("module.compute.google_project_iam_member.reaper", "google_project_iam_member",
					map[string]any{"role": "roles/fugaroReaper", "member": "serviceAccount:fugaro-reaper@proj-1234.iam.gserviceaccount.com"}),
			),
			want: true,
		},
		{
			name: "a 404 on a resource that is not an IAM grant",
			err:  applyErr(withResource("module.installation.google_storage_bucket.runs", "Error creating bucket: googleapi: Error 404: Resource 'fugaro-runs-proj-1234' of kind 'STORAGE_BUCKET' does not exist, notFound")),
			plan: credentialsServicePlan(),
		},
		{
			// The grant is real and the 404 is real; the target is not
			// something this apply made, so a retry changes nothing.
			name: "a 404 on a grant whose target this apply does not create",
			err:  applyErr(withResource("module.installation.google_cloud_run_v2_service_iam_member.credentials_invoker", invoker404)),
			plan: planOf(
				create("module.installation.google_cloud_run_v2_service_iam_member.credentials_invoker", "google_cloud_run_v2_service_iam_member",
					map[string]any{"name": "fugaro-credentials", "role": "roles/run.invoker"}),
			),
		},
		{
			// One propagation error is not the whole apply: what else failed
			// is what a retry would hide.
			name: "one 404 mixed with another failure",
			err: applyErr(withResource("module.installation.google_cloud_run_v2_service_iam_member.credentials_invoker", invoker404) +
				withResource("module.installation.google_cloud_run_v2_job.history", "Error creating job: googleapi: Error 403: Quota exceeded, quotaExceeded")),
			plan: credentialsServicePlan(),
		},
		{
			name: "a plan that failed, not an apply",
			err:  &ExitError{Command: "plan", Code: 1, Stderr: withResource("module.installation.google_cloud_run_v2_service_iam_member.credentials_invoker", invoker404)},
			plan: credentialsServicePlan(),
		},
		{
			name: "a propagation error with no resource named",
			err:  applyErr("Error: " + invoker404 + "\n"),
			plan: credentialsServicePlan(),
		},
		{
			name: "an apply that printed no error at all",
			err:  applyErr("module.installation.google_cloud_run_v2_service.credentials[0]: Creation complete after 11s\n"),
			plan: credentialsServicePlan(),
		},
		{
			// The tail is all fugaro keeps of stderr, and a diagnostic the
			// tail cut in half is not evidence that every error was this one.
			name: "stderr fugaro only kept the tail of",
			err:  &ExitError{Command: "apply", Code: 1, Truncated: true, Stderr: withResource("module.installation.google_cloud_run_v2_service_iam_member.credentials_invoker", invoker404)},
			plan: credentialsServicePlan(),
		},
		{
			name: "an error that is not terraform's at all",
			err:  errors.New("googleapi: Error 404: Resource 'fugaro-credentials' of kind 'SERVICE' does not exist"),
			plan: credentialsServicePlan(),
		},
	}
	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			if got := PropagationFailure(c.err, c.plan); got != c.want {
				t.Errorf("PropagationFailure = %v, want %v", got, c.want)
			}
		})
	}
}

// The box and the wrapping are terraform's own shape: -no-color boxes every
// diagnostic and wraps its detail at the terminal's width, so a message can
// arrive split across lines. A classifier that read only an unboxed one-line
// error would never fire on a real apply.
func TestPropagationFailureReadsTerraformsBoxedOutput(t *testing.T) {
	boxed := "╷\n" +
		"│ Error: Error applying IAM policy for project \"proj-1234\": Error\n" +
		"│ setting IAM policy for project \"proj-1234\": googleapi: Error 400:\n" +
		"│ Service account fugaro-reaper@proj-1234.iam.gserviceaccount.com does\n" +
		"│ not exist., badRequest\n" +
		"│ \n" +
		"│   with module.compute.google_project_iam_member.reaper,\n" +
		"│   on main.tf line 42, in resource \"google_project_iam_member\" \"reaper\":\n" +
		"│   42: resource \"google_project_iam_member\" \"reaper\" {\n" +
		"╵\n"
	plan := planOf(create("module.compute.google_service_account.reaper", "google_service_account",
		map[string]any{"account_id": "fugaro-reaper"}))
	if !PropagationFailure(applyErr(boxed), plan) {
		t.Fatalf("a boxed, wrapped 400 was not read as the gap it is:\n%s", boxed)
	}
}

// The kind and the name are read out of the message, and the name is what the
// plan is searched for: a 404 that names one resource while this apply created
// another is not this apply's own gap.
func TestPropagationFailureMatchesTheCreatedResource(t *testing.T) {
	other := planOf(create("module.installation.google_cloud_run_v2_service.other[0]", "google_cloud_run_v2_service",
		map[string]any{"name": "fugaro-other"}))
	err := applyErr(withResource("module.installation.google_cloud_run_v2_service_iam_member.credentials_invoker", invoker404))
	if PropagationFailure(err, other) {
		t.Error("a 404 naming a service this apply did not create was retried")
	}
}

// The plan the classifier reads is the one the apply was on; without one there
// is nothing to say the target was created in the same apply.
func TestPropagationFailureWithoutAPlan(t *testing.T) {
	err := applyErr(withResource("module.installation.google_cloud_run_v2_service_iam_member.credentials_invoker", invoker404))
	if PropagationFailure(err, nil) {
		t.Error("a 404 was retried with no plan to judge it against")
	}
}

// The error the wrapper returns for an apply is an ExitError, and callers wrap
// it (remote() is one); the classifier has to see through the wrapping.
func TestPropagationFailureSeesThroughWrapping(t *testing.T) {
	wrapped := wrappedErr{applyErr(withResource("module.installation.google_cloud_run_v2_service_iam_member.credentials_invoker", invoker404))}
	if !PropagationFailure(wrapped, credentialsServicePlan()) {
		t.Error("a wrapped apply error was not classified")
	}
}

type wrappedErr struct{ err error }

func (w wrappedErr) Error() string { return w.err.Error() }
func (w wrappedErr) Unwrap() error { return w.err }

// The plan's own change attributes are the ones terraform writes, so a fixture
// that names a resource the way the plan does is what the search has to read.
func TestPropagationFailureReadsThePlansOwnAttributes(t *testing.T) {
	raw := []byte(`{"format_version":"1.2","resource_changes":[` +
		`{"address":"module.installation.google_cloud_run_v2_service.credentials[0]","mode":"managed","type":"google_cloud_run_v2_service",` +
		`"change":{"actions":["create"],"before":{},"after":{"name":"fugaro-credentials","location":"us-east5"}}}]}`)
	var p Plan
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if !PropagationFailure(applyErr(withResource("module.installation.google_cloud_run_v2_service_iam_member.credentials_invoker", invoker404)), &p) {
		t.Error("the plan's own decoded attributes were not read")
	}
}
