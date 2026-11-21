package runner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/identitywire"
)

// batchRunEnv is a job environment of a run on Cloud Batch: the launcher
// writes FUGARO_BACKEND on every job it creates, and a Batch job says
// cloud-batch (internal/backend/batch's jobVariables).
func batchRunEnv(metadataHost string) []string {
	return []string{
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

// TestTheAttestationIsNeverPrinted: the attestation is this VM's own
// credential, so every reason the run publishes from the moment it is read is
// redacted of it (BR4, R7).
func TestTheAttestationIsNeverPrinted(t *testing.T) {
	const tok = "eyJhbGciOiJSUzI1NiJ9.a-vm-identity-token-in-full-format.sig"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(tok))
	}))
	defer srv.Close()

	r := &run{d: Deps{Env: batchRunEnv(strings.TrimPrefix(srv.URL, "http://"))}}
	got, err := r.batchAttestation(context.Background(), "https://creds-service-abc-ew.a.run.app")
	if err != nil {
		t.Fatalf("attesting this VM: %v", err)
	}
	line := "the claim was refused: " + got + " is not a VM of this job"
	if printed := r.redact(line); strings.Contains(printed, got) {
		t.Errorf("a reason the run publishes carries the attestation: %q", printed)
	}
	if printed := r.redact(line); !strings.Contains(printed, "[REDACTED]") {
		t.Errorf("the attestation was not redacted but dropped whole: %q", printed)
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
