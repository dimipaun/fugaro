//go:build firebase

package rules

// The build lease's own key space (T8 of docs/no-prompt-policy/
// implementation-plan.md; D1543, F19, F22): an image build reserves its
// estimated cost under buildleases/<slug>/<id>, never under runs/<slug>/<run>,
// so no record reader — runstore, watch, the console — mistakes a lease for a
// run, and the document names that family as a refusal instead of leaving it to
// the root's own `.read: false`/`.write: false`.
//
// Why the family is *refused* and not granted. Its writers are IAM identities:
// the CLI takes and closes a lease on the person's own credentials
// (internal/cli/cloud.go's newBudgetClient, ADC with the budget scopes) and the
// project's catalog-check job takes its own on the job's service account, and
// the rules bind neither — a client token is not what either side holds. The
// only client token the document knows how to bound is a run's (fs/fr, RUN),
// and a build lease is not taken inside a run: nothing in the container submits
// a build. So the family is written out closed, which is the shape
// internal/projectpolicy's RulesVersion doc calls "a denial, or a node no client
// may reach", and what these two tests pin is that the refusal is real and that
// it opens nothing anywhere else.
//
// The name is half the assertion, and it is the half the emulator cannot make:
// the root already refuses everything, so a family the template never mentions
// is refused by a rule nobody reading the file can see, and the next person who
// adds a grant to this document has to meet it. That half is hermetic —
// TestBuildLeaseFamilyIsTheWritersOwnName, in rules_test.go, reads the family
// out of the template and the generated document by name. What is left for the
// emulator is the deployed document itself: it refuses the family to the token
// that may reach the money nodes (a run's), while the CLI's own path, which is
// IAM and bypasses the rules, writes a lease there and reads it back — so the
// refusal is of the client and not of a path nothing uses.
//
// Hand mutation each fails on: giving the family a `.read` or a `.write` a
// client's token satisfies (a client can read the leases, or mint one), moving
// the family under `runs/` (a lease becomes a run the record readers see, and
// the token's own run rules then cover it), and renaming it away from
// budget.BuildLeasesRoot (the CLI's writer writes a family the document does
// not carry).

import (
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// buildLeaseValue is one lease as the CLI writes it: the value
// internal/cli's imageCost puts at budget.PathBuildLease. Amounts are
// micro-dollars and instants are epoch milliseconds, and it is written whole —
// the family's shape is the writer's, because no rule validates it.
func buildLeaseValue(slug, id string, reserved budget.Micros) budget.BuildLease {
	now := time.Now()
	return budget.BuildLease{
		State:       budget.BuildLeaseOpen,
		Repo:        slug,
		ID:          id,
		By:          "alice@example.invalid",
		MachineType: "e2-standard-2",
		Region:      "us-central1",
		SKUs:        []string{"1D2F-0F1D-3F1E"},
		PriceReadAt: now.UnixMilli(),
		Reserved:    reserved,
		StartedAt:   now.UnixMilli(),
		Exp:         budget.BuildLeaseDeadline(now).UnixMilli(),
	}
}

func TestBuildLeasePath(t *testing.T) {
	h := newHarness(t)
	h.seed(leased(2 * usd))
	run := h.run("aurora", "r1")
	// The token reaches a family the rules do grant it, so the refusals below
	// are refusals of this family rather than of an empty database.
	h.mustRead(run, budget.PathCapsGlobal)

	// The CLI's own path: the harness's admin client is IAM, which is what the
	// CLI writes with, and the lease it leaves is what the token below is
	// refused. A path nothing could write would pass every denial for the wrong
	// reason.
	h.set(budget.PathBuildLease("aurora", "b1"), buildLeaseValue("aurora", "b1", 2*usd))
	h.wantValue(budget.PathBuildLease("aurora", "b1")+"/state", `"open"`)
	h.wantValue(budget.PathBuildLease("aurora", "b1")+"/reserved", "2000000")
	// And the lease is not a run: it sits at its own key, so nothing that reads
	// runs/ sees one (F22).
	h.wantValue(budget.PathRun("aurora", "b1"), "null")

	for _, p := range []string{
		budget.PathBuildLeases("aurora"),
		budget.PathBuildLease("aurora", "b1"),
		budget.PathBuildLease("aurora", "b1") + "/reserved",
		budget.PathBuildLeases("other"),
	} {
		h.mustReadDenied(run, p)
	}
	for _, c := range []struct {
		who   string
		write func() map[string]any
	}{
		{"a run's token", func() map[string]any {
			return put(budget.PathBuildLease("aurora", "b2"), buildLeaseValue("aurora", "b2", usd))
		}},
		{"a run's token", func() map[string]any {
			return put(budget.PathBuildLease("aurora", "b1")+"/reserved", int64(3*usd))
		}},
		{"a run's token", func() map[string]any {
			return put(budget.PathBuildLeases("other")+"/b1/reserved", int64(usd))
		}},
		{"a run's token", func() map[string]any { return put(budget.PathBuildLease("aurora", "b1"), nil) }},
	} {
		h.mustDeny(run, c.who+" writing a build lease", c.write())
	}
	h.mustDeny(h.run("other", "r9"), "another repository's run writing a build lease",
		put(budget.PathBuildLease("other", "b3"), buildLeaseValue("other", "b3", usd)))
	// The lease the CLI wrote is untouched by every one of those refusals.
	h.wantValue(budget.PathBuildLease("aurora", "b1")+"/state", `"open"`)
	h.wantValue(budget.PathBuildLease("aurora", "b1")+"/reserved", "2000000")
}

func TestBuildLeaseWriterCannotTouchAnotherRunsReservedOrCaps(t *testing.T) {
	// Naming the family in the document is what scopes the lease path; the
	// other half of F22 is that the name reaches nothing else. The writer is
	// IAM, so no rule addresses it at all — what the rules can address is the
	// run token of the repository whose lease it is, and that token is what a
	// widening of this family would hand the ledger to. Every write below is
	// one such a token still must not be able to make, lease path or not.
	for _, tc := range []struct {
		name  string
		who   string
		token func(h *harness) *rtdb.Client
		write func(w world) map[string]any
	}{
		{
			name:  "another run's reserved",
			who:   "a run of the same repository, with another run id",
			token: func(h *harness) *rtdb.Client { return h.run("aurora", "r2") },
			write: func(w world) map[string]any {
				return put(budget.PathRun("aurora", "r1")+"/reserved", int64(usd))
			},
		},
		{
			name:  "another run's released",
			who:   "a run of the same repository, with another run id",
			token: func(h *harness) *rtdb.Client { return h.run("aurora", "r2") },
			write: func(w world) map[string]any {
				return put(budget.PathRun("aurora", "r1")+"/released", int64(usd))
			},
		},
		{
			name:  "another repository's run",
			who:   "a run of another repository",
			token: func(h *harness) *rtdb.Client { return h.run("other", "r1") },
			write: func(w world) map[string]any {
				return put(budget.PathRun("aurora", "r1")+"/reserved", int64(usd))
			},
		},
		{
			name:  "another run's day share",
			who:   "a run of the same repository, with another run id",
			token: func(h *harness) *rtdb.Client { return h.run("aurora", "r2") },
			write: func(w world) map[string]any {
				return put(budget.PathSpendRun(w.day, "aurora", "r1")+"/reserved", int64(usd))
			},
		},
		{
			name:  "the project's caps",
			who:   "a run's token",
			token: func(h *harness) *rtdb.Client { return h.run("aurora", "r1") },
			write: func(w world) map[string]any {
				return put(budget.PathCapsGlobal+"/dailyMicros", int64(1000*usd))
			},
		},
		{
			name:  "the repository's caps",
			who:   "a run's token",
			token: func(h *harness) *rtdb.Client { return h.run("aurora", "r1") },
			write: func(w world) map[string]any {
				return put(budget.PathCapsRepo("aurora")+"/dailyMicros", int64(1000*usd))
			},
		},
		{
			name:  "the mode",
			who:   "a run's token",
			token: func(h *harness) *rtdb.Client { return h.run("aurora", "r1") },
			write: func(w world) map[string]any { return put(budget.PathMode, budget.ModeObserve) },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			w := stdWorld()
			h.seed(w)
			client := tc.token(h)
			// The control: this token reaches a node the rules grant it, so
			// the refusal below is the rule's and not an empty database's.
			h.mustRead(client, budget.PathCapsGlobal)
			h.mustDeny(client, tc.who+" writing "+tc.name, tc.write(w))
		})
	}
}
