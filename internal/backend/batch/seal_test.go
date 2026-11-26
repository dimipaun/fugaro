package batch

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/identitywire"
)

// ticket is the value these tests' launches carry: one printable value of the
// length the launcher's own gate accepts (launchTicketUsable), and never a real
// credential -- nothing here is a secret-shaped literal.
func ticket() string { return strings.Repeat("t", 43) }

// TestABatchLaunchCarriesTheTicketAndNothingElse is BR1's acceptance at the
// backend (R1, R8): the run's identity reaches the job as the ticket, in the
// job's own plain variables, and the job carries NOTHING else of the launch's
// secrets -- no secretVariables, no encryptedVariables, no sealed entry.
//
// Its red step is the pair the plan names: the plain budget marker appearing
// among the job's variables, or the ticket travelling anywhere but the job.
//
// The ticket is a plain variable and not a secret one on purpose (BR1, the
// launcher's own write in run.go): a Batch job's secret carrier is the job
// definition -- the leak the marker's refusal below exists to keep out of the
// job resource -- and the ticket is a one-time value for one execution, which
// is what it is handed to.
func TestABatchLaunchCarriesTheTicketAndNothingElse(t *testing.T) {
	ctx := context.Background()
	b, bf, _, _ := newTestBackend(t, nil)
	s := spec()
	s.Env = []string{identitywire.TicketEnv + "=" + ticket()}
	if _, err := b.Launch(ctx, s); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	creates := bf.Creates()
	if len(creates) != 1 {
		t.Fatalf("jobs.create calls = %d, want 1", len(creates))
	}
	vars := object(t, creates[0].Job, "taskGroups", "0", "taskSpec", "environment", "variables")
	if got := vars[identitywire.TicketEnv]; got != ticket() {
		t.Fatalf("the task's %s = %v, want the ticket: a Batch launch gives its run the ticket and nothing else does", identitywire.TicketEnv, got)
	}
	env := object(t, creates[0].Job, "taskGroups", "0", "taskSpec", "environment")
	for _, field := range []string{"secretVariables", "encryptedVariables"} {
		if got, ok := env[field]; ok {
			t.Errorf("the job carries %v under %s: a Batch job's secret carrier is the job definition itself, and BR1's launch puts the ticket there as a plain variable instead", got, field)
		}
	}
	// The plain budget marker is never a job variable (BR1, F21): the seal
	// key's value is the marker's, and a job's variables are readable by every
	// launcher the job-name condition admits and by the reaper for as long as
	// the job is kept. A launch that spells it plainly is refused, exactly as a
	// sealed one is, and nothing is created either way.
	m, _, _, _ := newTestBackend(t, nil)
	mark := spec()
	mark.Env = []string{gitops.BudgetTokenEnv + "=" + strings.Repeat("k", 43)}
	if _, err := m.Launch(ctx, mark); !errors.Is(err, backend.ErrRejected) {
		t.Fatalf("Launch carrying %s plainly = %v, want ErrRejected: the marker's value IS the seal key, and it is never a variable of a Batch job", gitops.BudgetTokenEnv, err)
	}
}

// TestASealedBatchLaunchIsRefused is AB9's rule at BR1's rewrite (F21, R8): a
// Batch launch with a sealed entry is refused, and the refusal says what the
// new one says -- that this is about the budget token alone, and that a Batch
// run's identity travels as its ticket.
//
// The sentence is pinned by three readers at once
// (TestDocsBudgetTokenChannelAnswersTheBatchSeal and the two documents), so
// this test holds the one thing the code owes the pin: the phrase is really in
// what an operator reads, and it closes the sentence rather than replacing the
// limit.
func TestASealedBatchLaunchIsRefused(t *testing.T) {
	ctx := context.Background()
	b, bf, _, warns := newTestBackend(t, nil)
	key := strings.Repeat("k", 43)
	s := spec()
	s.SecretEnv = []string{gitops.BudgetTokenEnv + "=" + key}
	_, err := b.Launch(ctx, s)
	if !errors.Is(err, backend.ErrRejected) {
		t.Fatalf("Launch with a sealed entry = %v, want ErrRejected", err)
	}
	if strings.Contains(err.Error(), key) {
		t.Fatalf("the refusal quotes the seal key: %v", err)
	}
	if !strings.Contains(err.Error(), gitops.BudgetTokenEnv) {
		t.Errorf("the refusal does not name the entry it refused: %v", err)
	}
	for _, want := range []string{
		"a Batch run's identity travels as its ticket",
		"cannot launch any run on Batch",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not carry %q, which the two documents and their test pin: %v", want, err)
		}
	}
	if n := len(bf.Creates()); n != 0 {
		t.Fatalf("a sealed launch created %d job(s): the key would be a standing copy in the job resource", n)
	}
	for _, got := range *warns {
		if strings.Contains(got, key) {
			t.Fatalf("a warning quotes the seal key: %s", got)
		}
	}
}
