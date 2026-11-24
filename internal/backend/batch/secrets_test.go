package batch

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestABatchJobMountsNothing is BR11's acceptance, at the backend: a Batch job
// carries no secret at all (D3104, D3107). A run launched on this backend gets
// its credentials from the credentials service alone, exactly as a Cloud Run
// run does, and the block it was told about is a block the launcher reads and
// mounts nothing from (R10) -- so the job this package builds for an
// installation whose block still carries `batch.secrets` is the job it builds
// for one that carries none, byte for byte.
//
// The assertions are one fact seen three ways. The field is absent, so nothing
// of the map is on the job's own environment -- that is the red step: before
// BR11 a job with `batch.secrets` set carried `secretVariables`. The secret's
// name appears nowhere in the serialized job, which is the half a reader of the
// API would see: the name is a standing grant on the secret for whoever holds
// `batch.jobs.get`, and it is what the credentials service replaced. And the
// identity variable is the job's own, unchanged: a map whose entry is named
// FUGARO_RUN used to be refused as a collision (both fields were one
// environment on the VM), and with nothing mounted it collides with nothing, so
// the job is built and carries the run's own identity.
//
// Hand mutations, each red: mount the map again (the line BR11 removed,
// `env.SecretVariables = b.o.Secrets`), or write it into the plain variables
// instead -- the leak §1.6, C10 keeps out of the job resource, and the one a
// test that only looked for the field would miss.
func TestABatchJobMountsNothing(t *testing.T) {
	ctx := context.Background()
	// The job of the same launch on an installation whose block carries no
	// secret: what every job below has to be, byte for byte.
	plain := createdJobJSON(t, spec(), nil)

	b, bf, _, _ := newTestBackend(t, func(o *Options) {
		o.Secrets = map[string]string{"ANTHROPIC_API_KEY": testSecret}
	})
	if _, err := b.Launch(ctx, spec()); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	creates := bf.Creates()
	if len(creates) != 1 {
		t.Fatalf("jobs.create calls = %d, want 1", len(creates))
	}
	env := object(t, creates[0].Job, "taskGroups", "0", "taskSpec", "environment")
	if got, ok := env["secretVariables"]; ok {
		t.Errorf("the job carries %v under secretVariables, want nothing mounted: a Batch run's credentials come from the credentials service alone (D3104)", got)
	}
	raw, err := json.Marshal(creates[0].Job)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), testSecret) {
		t.Errorf("the secret's name is on the job: the job resource is readable by anyone with batch.jobs.get, and a name there is a standing grant on the secret the service carries instead (design compute-backend.md §1.6, C10): %s", raw)
	}
	if got := createdJobJSON(t, spec(), func(o *Options) {
		o.Secrets = map[string]string{"ANTHROPIC_API_KEY": testSecret}
	}); got != plain {
		t.Errorf("a launch on an installation whose block carries a secret is not the launch it is without one:\n%s\n\nwant:\n%s", got, plain)
	}

	// A map naming the run's identity variable, which the mount used to make a
	// collision rather than a job: built, and the variables are the job's own.
	if got := createdJobJSON(t, spec(), func(o *Options) {
		o.Secrets = map[string]string{"FUGARO_RUN": testSecret}
	}); got != plain {
		t.Errorf("a launch whose block names FUGARO_RUN is not the launch whose block names nothing:\n%s\n\nwant:\n%s", got, plain)
	}
}
