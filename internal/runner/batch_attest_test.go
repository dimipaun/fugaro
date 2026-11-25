package runner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/identitywire"
)

// batchRunEnv is a job environment of a run on Cloud Batch: the pair Batch
// injects into every task, BATCH_TASK_INDEX and BATCH_TASK_COUNT, which is what
// says a process is one of Batch's tasks (backend.DetectPlatform, the predicate
// batchAttestation reads), beside FUGARO_BACKEND, which the launcher writes
// into every job it creates and a Batch job says cloud-batch in
// (internal/backend/batch's jobVariables).
func batchRunEnv(metadataHost string) []string {
	return []string{
		"BATCH_TASK_INDEX=0", "BATCH_TASK_COUNT=1",
		backend.BackendEnv + "=" + backend.CloudBatch,
		metadataHostEnv + "=" + metadataHost,
	}
}

// TestABatchRunAttestsItsOwnVM: a run on Batch reads its VM's identity token
// in the metadata server's full format — the format that carries the
// instance's id, name, project and zone, which is the fact the service's link
// is made of (U2) — and a run that is not on Batch reads none at all, which is
// what a Cloud Run claim carries (C1).
func TestABatchRunAttestsItsOwnVM(t *testing.T) {
	const audience = "https://creds-service-abc-ew.a.run.app"
	const tok = "eyJhbGciOiJSUzI1NiJ9.a-vm-identity-token-in-full-format.sig"

	t.Run("on Batch", func(t *testing.T) {
		var mu sync.Mutex
		var seen []*http.Request
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			seen = append(seen, r.Clone(r.Context()))
			mu.Unlock()
			_, _ = w.Write([]byte(tok + "\n"))
		}))
		defer srv.Close()

		r := &run{d: Deps{Env: batchRunEnv(strings.TrimPrefix(srv.URL, "http://"))}}
		got, err := r.batchAttestation(context.Background(), audience)
		if err != nil {
			t.Fatalf("attesting this VM: %v", err)
		}
		if got != tok {
			t.Errorf("the attestation is %q, want the metadata server's own (trailing whitespace trimmed)", got)
		}

		mu.Lock()
		defer mu.Unlock()
		if len(seen) != 1 {
			t.Fatalf("the metadata server was asked %d times, want once", len(seen))
		}
		if seen[0].URL.Path != metadataIdentityPath {
			t.Errorf("the path is %q, want %q", seen[0].URL.Path, metadataIdentityPath)
		}
		if aud := seen[0].URL.Query().Get("audience"); aud != audience {
			t.Errorf("the audience is %q, want %q", aud, audience)
		}
		// The full format is the whole of the attestation: the short one
		// carries none of the instance facts the service's link reads.
		if f := seen[0].URL.Query().Get("format"); f != attestationFormat {
			t.Errorf("the format is %q, want %q", f, attestationFormat)
		}
		if seen[0].Header.Get("Metadata-Flavor") != "Google" {
			t.Error("the request carries no Metadata-Flavor: Google")
		}
	})

	t.Run("not on Batch", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("a run that is not on Batch asked the metadata server for an attestation: %s", r.URL)
			_, _ = w.Write([]byte(tok))
		}))
		defer srv.Close()

		env := []string{
			backend.BackendEnv + "=cloud-run",
			metadataHostEnv + "=" + strings.TrimPrefix(srv.URL, "http://"),
		}
		r := &run{d: Deps{Env: env}}
		got, err := r.batchAttestation(context.Background(), audience)
		if err != nil {
			t.Fatalf("a Cloud Run run attesting: %v", err)
		}
		if got != "" {
			t.Errorf("a run that is not on Batch carries the attestation %q, want none", got)
		}
	})

	t.Run("a metadata server that refuses", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, tok, http.StatusForbidden)
		}))
		defer srv.Close()

		r := &run{d: Deps{Env: batchRunEnv(strings.TrimPrefix(srv.URL, "http://"))}}
		got, err := r.batchAttestation(context.Background(), audience)
		if err == nil {
			t.Fatalf("the read returned %q", got)
		}
		// Whatever the metadata server answered appears in no error the run
		// can log or record.
		if strings.Contains(err.Error(), tok) {
			t.Errorf("the error carries what the metadata server answered: %v", err)
		}
	})
}

// The attestation's own leak check is not here: it is the runner_test half's
// TestTheAttestationIsNeverPrinted (batch_claim_test.go), which reads what a
// whole run publishes around a claim that really carried the value -- the
// record, the halt's detail and the run's log lines -- rather than what one
// call to the redactor makes of a line this test wrote.

// TestTheTicketNeverReachesTheSession: the ticket arrives in the job's
// environment — a Batch launch sets it as a job variable — and the run's own
// stages are built from an allow-list that holds no FUGARO_* name
// (internal/agent's BuildEnv, docs/agent-env/spec.md §4), so the session never
// carries it. It is pinned here because the ticket is a new name and an
// allow-list that grew one would be a change nothing else would notice
// (BR4's acceptance: the ticket is stripped from the session's environment).
func TestTheTicketNeverReachesTheSession(t *testing.T) {
	const value = "ticket-fixture-never-a-real-ticket"
	parent := append(batchRunEnv("127.0.0.1:1"), identitywire.TicketEnv+"="+value, "PATH=/bin")
	env, _, err := agent.BuildEnv(parent, agent.EnvSpec{Auth: "api-key",
		Gateway: &agent.Gateway{BaseURL: "http://127.0.0.1:1", Token: gatewayToken}})
	if err != nil {
		t.Fatal(err)
	}
	// The environment is a real one: an allow-listed variable of the job's
	// reaches the session, so the loop below is not passing over nothing.
	if !slices.Contains(env, "PATH=/bin") {
		t.Fatalf("the session's environment lost an allow-listed variable: %v", env)
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, identitywire.TicketEnv+"=") || strings.Contains(kv, value) {
			t.Errorf("the session's environment carries the ticket: %q", kv)
		}
	}
}

// TestTheTicketHasOneName: the variable a Batch launch carries its ticket in
// is one name, spelled in the contract (identitywire.TicketEnv) and in the
// package that strips it from every git call (gitops.IdentityTicketEnv, which
// cannot import the contract because the contract depends on it). A drift
// between the two would leave a run's ticket in a git call's environment
// without a word.
func TestTheTicketHasOneName(t *testing.T) {
	if gitops.IdentityTicketEnv != identitywire.TicketEnv {
		t.Fatalf("gitops.IdentityTicketEnv is %q and identitywire.TicketEnv is %q, want one name",
			gitops.IdentityTicketEnv, identitywire.TicketEnv)
	}
}
