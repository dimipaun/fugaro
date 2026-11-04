package cli

// This file holds the part of `fugaro report` that stays MIT (D444, T11). The
// command, its renderer and the report library moved to ee/report in E2.2b, but
// core code reads these seven symbols — diagnose prints a run's cost lines,
// `fugaro update` opens the spend history to check it, and the run-record
// totals and the by-mode rows divide a run's dollars the same way — so a moved
// copy would be a second implementation of a split the core already owns. The
// moved command reaches them through api.go: Cloud.Firestore, Cloud.FirestoreErr
// and the exported RecordOfRun, ComputeSources and ApplyCost wrappers, which
// call these unchanged.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/firestore"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// lazyFacts opens the runs bucket only when there is something to read. It
// stays with the core because `budget show --compute` reads it too; the moved
// report command reaches it through api.go's LiveFacts, which is this reader
// unchanged.
type lazyFacts struct {
	lc   *localcfg.Config
	warn func(string)
	// slugs narrows the pass to these repositories; empty reads every one in
	// the bucket (budget.BucketFacts).
	slugs []string
	env   *cloudEnv
}

func (l *lazyFacts) Facts(ctx context.Context, from, to int64) (map[int64][]budget.RunFact, map[string]string, error) {
	env, err := openCloudFor(ctx, l.lc)
	if err != nil {
		return nil, nil, err
	}
	l.env = env
	return budget.BucketFacts{Bucket: env.bucket.Bucket, Warn: l.warn, Slugs: l.slugs, Backend: l.lc.Backend()}.Facts(ctx, from, to)
}

func (l *lazyFacts) close() {
	if l.env != nil {
		l.env.Close()
	}
}

// newReportFirestore is the client of a project's spend history, read as the
// person: roles/datastore.viewer on the Firebase project.
func newReportFirestore(ctx context.Context, lc *localcfg.Config) (*firestore.Client, error) {
	var src oauth2.TokenSource
	if lc.Endpoints.NoAuth {
		if lc.Endpoints.Firestore == "" {
			return nil, userErr("no_auth is set but endpoints.firestore is empty: refusing to reach the real Firestore without credentials")
		}
		src = oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test"})
	} else {
		ts, err := google.DefaultTokenSource(ctx, cloudPlatform)
		if err != nil {
			return nil, userErr("no Google credentials to reach the spend history: run gcloud auth application-default login (%v)", err)
		}
		src = ts
	}
	fs, err := firestore.New(lc.Endpoints.Firestore, lc.Budget.FirebaseProject, src)
	if err != nil {
		return nil, userErr("%v", err)
	}
	return fs, nil
}

// firestoreErr classifies a Firestore read failure: a refusal names the role.
func firestoreErr(lc *localcfg.Config, err error) error {
	if errors.Is(err, firestore.ErrPermission) {
		return userErr("project %s's spend history refused the read: you need the Cloud Datastore Viewer role (roles/datastore.viewer) on its Firebase project %s, plus the Service Usage Consumer role (roles/serviceusage.serviceUsageConsumer); GCP owners and editors have both; gcloud auth application-default login may be needed (%s)",
			lc.Name, safetext.Strip(lc.Budget.FirebaseProject), safetext.Strip(err.Error()))
	}
	return remote(fmt.Errorf("reading the spend history: %s", safetext.Strip(err.Error())))
}

// computeSources is the distinct price-table source lines the rows' compute
// estimates were priced from, sorted, for the report's own source line. Rows
// priced with a table that does not name itself (Cloud Run's list price, and
// every run recorded before this existed) contribute nothing, so the report
// of an installation that never ran on a VM reads exactly as it did.
func computeSources(rows []runview.Row) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range rows {
		if src := r.Cost.ComputeSource; src != "" && !seen[src] {
			seen[src] = true
			out = append(out, src)
		}
	}
	sort.Strings(out)
	return out
}

// costLines is the cost block diagnose prints: the cost line, and under it
// the compute estimate's own source line when it has one (a VM run's price
// table, design compute-backend.md §5.2). One line per line, since oneLine
// would turn the break between them into a "?" and the two lines are not one
// sentence.
func costLines(c runstore.Cost) []string {
	var out []string
	for _, ln := range strings.Split(strings.ReplaceAll(runner.CostLine(c), "**", ""), "\n") {
		out = append(out, oneLine(ln))
	}
	return out
}

// microsOfUSD converts a USD figure to the history's micro-dollars, treating
// a figure that is not a positive number as zero.
func microsOfUSD(v float64) budget.Micros {
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return 0
	}
	return budget.Micros(math.Round(v * 1e6))
}

// applyCost writes a run's cost into a one-run day record: the two halves of
// the model figure (P12, D140), so a mixed cascade run's tier-A dollars are
// notional in the history and a subscription run's whole figure still is. One
// split for both readers of a run record -- the run-record totals and the
// by-mode rows -- so the two divide a mixed run the same way.
func applyCost(rec *budget.DayRecord, c runstore.Cost) {
	rec.SpentMicros = microsOfUSD(c.ModelMoneyUSD())
	rec.NotionalMicros = microsOfUSD(c.ModelSubscriptionUSD())
	if c.ComputeEstimated {
		rec.ComputeMicros, rec.ComputeEstimatedRuns = microsOfUSD(c.ComputeUSD), 1
	}
}

// recordOfRun is one run as a one-run day record, so the run-record totals
// group like the history does.
func recordOfRun(r runview.Row) budget.DayRecord {
	rec := budget.DayRecord{
		Repo: r.Repo, Slug: r.Repo, Date: budget.DayDate(budget.Day(r.Created.UTC())), Runs: 1,
		Outcomes: map[string]int64{}, ByModel: map[string]budget.ModelTotals{}, ByPerson: map[string]budget.PersonTotals{},
	}
	applyCost(&rec, r.Cost)
	spent, notional := rec.SpentMicros, rec.NotionalMicros
	// The by-model split of a run with a notional part is of its whole model
	// figure and cannot be told apart per model, so it is left out rather
	// than counted as money (a subscription run always had none of it here).
	// The split is asked of the notional part, never of the basis (P12,
	// D140): a cascade run the plan served in full is api-list and billed
	// nothing.
	for name, usd := range r.Cost.ModelBy {
		if r.Cost.AllNotional() || notional > 0 {
			continue
		}
		rec.ByModel[budget.Key(name)] = budget.ModelTotals{Micros: microsOfUSD(usd)}
	}
	who := r.RequestedBy
	if strings.TrimSpace(who) == "" {
		who = budget.UnknownPerson
	}
	rec.ByPerson[budget.Key(who)] = budget.PersonTotals{Micros: spent, NotionalMicros: notional, Runs: 1}
	return rec
}
