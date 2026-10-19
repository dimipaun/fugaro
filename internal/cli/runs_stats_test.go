package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
	"github.com/dimipaun/fugaro/internal/userstats"
)

// fugaro runs stats --by user. The command's whole value is that a team can
// read what each person started, so these tests hold the four things the
// ruling turns on:
//
//   - attribution: the launcher each run recorded is its group, an automated
//     identity is one of its own, and a run that recorded nobody is 'unknown'
//     and is never guessed from the task beside it;
//   - the money: counted and notional are separate columns and are never
//     summed, and neither is a run that has no readable record;
//   - the visibility ruling: an operator sees everyone, a plain launcher sees
//     their own row and may opt in, and an identity that cannot be placed
//     fails to the more private view;
//   - the read: bounded, with a truncation the output admits to.
//
// No fixture here names an installation other than the synthetic one the
// cloud fixture builds (reconciliation Global Constraint 10).

// userStatsPlace writes the fixture's local config with user: replaced by
// user (dropped when empty) and the installation's own launchers and
// operators lists set, so a test decides how the installation places the
// caller.
func userStatsPlace(t *testing.T, f *cloudFixture, user string, launchers, operators []string) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(data)
	line := ""
	if user != "" {
		line = "user: " + user + "\n"
	}
	cfg = regexp.MustCompile(`(?m)^user: .*\n`).ReplaceAllString(cfg, line)
	if len(launchers) > 0 || len(operators) > 0 {
		cfg += "terraform:\n"
		for _, l := range []struct {
			key  string
			list []string
		}{{"launchers", launchers}, {"operators", operators}} {
			if len(l.list) == 0 {
				continue
			}
			cfg += "  " + l.key + ": [" + strings.Join(l.list, ", ") + "]\n"
		}
	}
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

// seedUserRun writes one run's launch.json (with the launcher it recorded, ""
// for a launch that recorded nobody) and, when rec is not nil, its
// result.json.
func seedUserRun(t *testing.T, f *cloudFixture, id, launchedBy string, rec *runstore.Record) {
	t.Helper()
	ctx := context.Background()
	s := runstore.Open(statsBucket(t, f), appSlug, id)
	if err := s.WriteLaunch(ctx, &runstore.Launch{
		Version: 1, RunID: id, LaunchedBy: launchedBy, LaunchedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if rec == nil {
		return
	}
	rec.RunID = id
	if err := s.WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
}

// userRec is a settled result.json: a status, model dollars on one basis, and
// the cost breakdown that says which basis, built by the writer the runner
// itself uses so the fixture's TotalUSD means what a record's means.
func userRec(status runstore.Status, modelUSD float64, basis string) *runstore.Record {
	return userRecCost(status, modelUSD, 0, basis)
}

// userRecCost is userRec for a run whose compute was estimated too: the
// breakdown then carries ModelUSD and ComputeUSD, and TotalUSD is what the run
// cost altogether -- model plus compute on a key, compute alone on a
// subscription, whose notional model figure is nobody's bill (runstore.NewCost).
func userRecCost(status runstore.Status, modelUSD, computeUSD float64, basis string) *runstore.Record {
	at := time.Now().UTC().Add(-time.Hour)
	done := at.Add(time.Minute)
	cost := runstore.NewCost(modelUSD, computeUSD, basis)
	return &runstore.Record{
		Version: 1, Repo: "acme/app", Workflow: "web", Status: status,
		StartedAt: at, FinishedAt: &done,
		CostUSD: modelUSD,
		Cost:    &cost,
	}
}

// userStatsID is a run ID at 12:00:nn on day, so runs a test writes sort in
// the order it writes them (a run ID starts with its UTC mint time).
func userStatsID(day time.Time, n int) string {
	return fmt.Sprintf("%s-1200%02d-abcd", day.Format("20060102"), n)
}

// userStatsOut is runs stats --json's document, as a test reads it.
type userStatsOut struct {
	Project    string          `json:"project"`
	By         string          `json:"by"`
	View       string          `json:"view"`
	You        string          `json:"you"`
	Operator   bool            `json:"operator"`
	Since      string          `json:"since"`
	Until      string          `json:"until"`
	MaxRuns    int             `json:"max_runs"`
	Truncated  bool            `json:"truncated"`
	Rows       []userstats.Row `json:"rows"`
	Totals     []userstats.Row `json:"totals"`
	Unlaunched int             `json:"unlaunched"`
	Notes      []string        `json:"notes"`
}

// runsStats runs the command as JSON and returns its document.
func runsStats(t *testing.T, args ...string) userStatsOut {
	t.Helper()
	out, errOut, err := execute(t, append([]string{"runs", "stats", "--json"}, args...)...)
	if err != nil {
		t.Fatalf("runs stats: %v (%s)", err, errOut)
	}
	var got userStatsOut
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return got
}

// userStatsRow is the row a document carries for user, failing when there is
// none.
func userStatsRow(t *testing.T, doc userStatsOut, user string) userstats.Row {
	t.Helper()
	for _, r := range doc.Rows {
		if r.User == user {
			return r
		}
	}
	t.Fatalf("no row for %q in %+v", user, doc.Rows)
	return userstats.Row{}
}

// TestRunsStatsByUserAttributesEachRun is the attribution rule and the money
// rule together: two people, a service account with a row of its own, a run
// that recorded nobody, and a notional run that is a column of its own.
func TestRunsStatsByUserAttributesEachRun(t *testing.T) {
	f := newCloudFixture(t)
	// The caller is an operator of the fixture's installation: the local
	// config's user: is listed in its terraform.operators, the list fugaro
	// doctor's bucket check reads.
	userStatsPlace(t, f, "someone@example.com", nil, []string{"user:someone@example.com"})
	day := time.Now().UTC()

	seedUserRun(t, f, userStatsID(day, 0), "dana@example.com", userRec(runstore.StatusSucceeded, 1.50, runstore.BasisAPIList))
	seedUserRun(t, f, userStatsID(day, 1), "dana@example.com", userRec(runstore.StatusFailed, 0.50, runstore.BasisAPIList))
	// An automated launch is an identity like any other, never folded into a
	// person's row -- and a subscription's model dollars are notional.
	seedUserRun(t, f, userStatsID(day, 2), "fugaro-ci@app.iam.gserviceaccount.com", userRec(runstore.StatusSucceeded, 2.00, runstore.BasisSubscription))
	// A launch that recorded nobody is 'unknown', whatever its record says.
	seedUserRun(t, f, userStatsID(day, 3), "", userRec(runstore.StatusHalted, 3.00, runstore.BasisAPIList))

	doc := runsStats(t)
	if doc.View != "all" || !doc.Operator || doc.You != "someone@example.com" {
		t.Fatalf("an operator's view = %+v", doc)
	}
	if doc.By != "user" || doc.Project != "aurora" {
		t.Errorf("by = %q, project = %q", doc.By, doc.Project)
	}
	if len(doc.Rows) != 3 {
		t.Fatalf("rows = %+v, want dana, the service account and unknown", doc.Rows)
	}
	dana := userStatsRow(t, doc, "dana@example.com")
	if dana.Runs != 2 || dana.Succeeded != 1 || dana.Failed != 1 || dana.CountedUSD != 2.00 || dana.CountedRuns != 2 {
		t.Errorf("dana = %+v", dana)
	}
	if dana.AvgCountedUSD != 1.00 || dana.NotionalUSD != 0 {
		t.Errorf("dana = %+v, want $1.00 a run and no notional", dana)
	}
	ci := userStatsRow(t, doc, "fugaro-ci@app.iam.gserviceaccount.com")
	if ci.CountedUSD != 0 || ci.NotionalUSD != 2.00 || ci.CountedRuns != 0 || ci.AvgCountedUSD != 0 {
		t.Errorf("the service account's row = %+v: a subscription's dollars are notional and are "+
			"averaged into no real-dollar figure", ci)
	}
	unknown := userStatsRow(t, doc, userstats.UnknownUser)
	if unknown.Runs != 1 || unknown.Halted != 1 || unknown.CountedUSD != 3.00 {
		t.Errorf("the unknown row = %+v", unknown)
	}
	if len(doc.Totals) != 1 {
		t.Fatalf("totals = %+v, want one", doc.Totals)
	}
	tot := doc.Totals[0]
	if tot.Runs != 4 || tot.CountedUSD != 5.00 || tot.NotionalUSD != 2.00 || tot.CountedRuns != 3 {
		t.Errorf("total = %+v, want 4 runs, $5.00 counted over 3 and $2.00 notional", tot)
	}
	if tot.AvgCountedUSD != 1.67 {
		t.Errorf("total average = %v, want 1.67", tot.AvgCountedUSD)
	}
	// The counts add up, which is how the table is read.
	if sum := tot.Succeeded + tot.Failed + tot.Halted + tot.InfraError + tot.Other + tot.NoRecord; sum != tot.Runs {
		t.Errorf("the total's counts add up to %d, not %d: %+v", sum, tot.Runs, tot)
	}
	if len(doc.Notes) == 0 {
		t.Error("the document carries no note about what it shows")
	}

	// The human table carries the same figures, and says whose they are.
	human, _, err := execute(t, "runs", "stats")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"dana@example.com", "every user of this installation", "TOTAL", "$5.00", "$2.00"} {
		if !strings.Contains(human, want) {
			t.Errorf("the table does not carry %q:\n%s", want, human)
		}
	}
	if strings.Contains(human, "\x1b") {
		t.Errorf("the table carries an escape:\n%q", human)
	}
}

// TestRunsStatsSpendIsModelDollarsNotCompute is the other half of the money
// rule: the columns are model spend, and a run's compute estimate is in
// neither of them. result.json's TotalUSD is what the run cost altogether, so
// a column read off it would publish a figure no budget screen counts and
// attribute Google's compute to a person, who is not charged for it.
func TestRunsStatsSpendIsModelDollarsNotCompute(t *testing.T) {
	f := newCloudFixture(t)
	userStatsPlace(t, f, "someone@example.com", nil, []string{"user:someone@example.com"})
	day := time.Now().UTC()
	// $1.50 of model on a key, and $0.20 of compute for the VM underneath it:
	// the record's total is $1.70, and only the model half is dana's spend.
	seedUserRun(t, f, userStatsID(day, 0), "dana@example.com",
		userRecCost(runstore.StatusSucceeded, 1.50, 0.20, runstore.BasisAPIList))
	// The same split on a subscription, where TotalUSD is the compute alone:
	// a notional model figure is left out of the bill.
	seedUserRun(t, f, userStatsID(day, 1), "fugaro-ci@app.iam.gserviceaccount.com",
		userRecCost(runstore.StatusSucceeded, 2.00, 0.30, runstore.BasisSubscription))

	doc := runsStats(t)
	dana := userStatsRow(t, doc, "dana@example.com")
	if dana.CountedUSD != 1.50 {
		t.Errorf("dana's counted spend = $%v, want $1.50: the run's $1.70 total is $1.50 of model "+
			"and $0.20 of compute, and compute is attributed to no person", dana.CountedUSD)
	}
	if dana.CountedRuns != 1 || dana.AvgCountedUSD != 1.50 {
		t.Errorf("dana's row = %+v, want one counted run averaged at $1.50", dana)
	}
	ci := userStatsRow(t, doc, "fugaro-ci@app.iam.gserviceaccount.com")
	if ci.NotionalUSD != 2.00 || ci.CountedUSD != 0 {
		t.Errorf("the service account's row = %+v, want $2.00 notional: a subscription's TotalUSD is "+
			"its compute, not the model spend the run recorded", ci)
	}
	if tot := doc.Totals[0]; tot.CountedUSD != 1.50 || tot.NotionalUSD != 2.00 {
		t.Errorf("total = %+v, want $1.50 counted and $2.00 notional: no compute in either column", tot)
	}

	// The table's money columns are the same two figures, so a reader adding
	// them cannot pick the compute up from beside them.
	human, _, err := execute(t, "runs", "stats")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human, "$1.50") || strings.Contains(human, "$1.70") {
		t.Errorf("the table should carry the model dollars alone, and never the run's $1.70 total:\n%s", human)
	}
}

// TestRunsStatsFollowUpBelongsToItsOwnLauncher is the follow-up rule: a
// follow-up carries the identity its own launch recorded, and the run it
// continues keeps its own -- two people on one pull request are two rows, and
// neither is credited with the other's run.
func TestRunsStatsFollowUpBelongsToItsOwnLauncher(t *testing.T) {
	f := newCloudFixture(t)
	userStatsPlace(t, f, "someone@example.com", nil, []string{"user:someone@example.com"})
	day := time.Now().UTC()
	pr := &runstore.PRRef{Number: 7, URL: "https://github.com/acme/app/pull/7"}
	first := userRec(runstore.StatusSucceeded, 4.00, runstore.BasisAPIList)
	first.PR = pr
	seedUserRun(t, f, userStatsID(day, 0), "dana@example.com", first)

	follow := userRec(runstore.StatusSucceeded, 1.00, runstore.BasisAPIList)
	follow.PR = pr
	follow.FollowUp = &runstore.FollowUp{PR: 7, PreviousRun: userStatsID(day, 0)}
	seedUserRun(t, f, userStatsID(day, 1), "sam@example.com", follow)

	doc := runsStats(t)
	if got := userStatsRow(t, doc, "dana@example.com").CountedUSD; got != 4.00 {
		t.Errorf("the continued run's launcher has %v, want 4.00 (the follow-up is not theirs)", got)
	}
	if got := userStatsRow(t, doc, "sam@example.com").CountedUSD; got != 1.00 {
		t.Errorf("the follow-up's launcher has %v, want 1.00", got)
	}
	if doc.Totals[0].Runs != 2 {
		t.Errorf("total = %+v, want both runs", doc.Totals[0])
	}
}

// TestRunsStatsNeverGuessesFromTheTask is the "never guessed" half of the
// attribution rule: a run whose task names a requester but whose launch
// recorded nobody is 'unknown', not that requester. Nothing of the task's own
// text reaches the output.
func TestRunsStatsNeverGuessesFromTheTask(t *testing.T) {
	f := newCloudFixture(t)
	userStatsPlace(t, f, "someone@example.com", nil, []string{"user:someone@example.com"})
	day := time.Now().UTC()
	id := userStatsID(day, 0)
	seedUserRun(t, f, id, "", userRec(runstore.StatusSucceeded, 1.00, runstore.BasisAPIList))
	// The task beside it names someone else entirely.
	if err := runstore.Open(statsBucket(t, f), appSlug, id).CreateTask(context.Background(), &task.Spec{
		Version: 1, RunID: id, Repo: "acme/app", Ref: "main", Workflow: "web",
		Task: "rewrite the billing pipeline", RequestedBy: "sam@example.com",
	}); err != nil {
		t.Fatal(err)
	}

	doc := runsStats(t)
	if len(doc.Rows) != 1 || doc.Rows[0].User != userstats.UnknownUser {
		t.Fatalf("rows = %+v, want one %s row: a task's requester is not the run's launcher", doc.Rows, userstats.UnknownUser)
	}
	raw, _ := json.Marshal(doc)
	if strings.Contains(string(raw), "sam@example.com") || strings.Contains(string(raw), "billing") {
		t.Errorf("the task's own text reached the output: %s", raw)
	}
}

// TestRunsStatsVisibilityIsTheRuling is the owner's ruling in all four of its
// cases: an operator sees everyone, a plain launcher sees their own row alone
// and may opt in, an installation whose lists place nobody stays private, and
// an identity that cannot be told at all is refused rather than shown
// everyone.
func TestRunsStatsVisibilityIsTheRuling(t *testing.T) {
	day := time.Now().UTC()
	seed := func(t *testing.T, f *cloudFixture) {
		t.Helper()
		seedUserRun(t, f, userStatsID(day, 0), "dana@example.com", userRec(runstore.StatusSucceeded, 1.00, runstore.BasisAPIList))
		seedUserRun(t, f, userStatsID(day, 1), "sam@example.com", userRec(runstore.StatusSucceeded, 2.00, runstore.BasisAPIList))
	}

	t.Run("an operator sees everyone", func(t *testing.T) {
		f := newCloudFixture(t)
		userStatsPlace(t, f, "dana@example.com", []string{"user:dana@example.com"}, []string{"user:dana@example.com"})
		seed(t, f)
		doc := runsStats(t)
		if doc.View != "all" || !doc.Operator || len(doc.Rows) != 2 {
			t.Fatalf("an operator's default view = %+v", doc)
		}
	})

	t.Run("a launcher sees their own row alone, and may opt in", func(t *testing.T) {
		f := newCloudFixture(t)
		// A launcher of the installation, and not an operator of it.
		userStatsPlace(t, f, "dana@example.com", []string{"user:dana@example.com"}, nil)
		seed(t, f)
		own := runsStats(t)
		if own.View != "own" || own.Operator {
			t.Fatalf("a launcher's default view = %+v", own)
		}
		if len(own.Rows) != 1 || own.Rows[0].User != "dana@example.com" {
			t.Fatalf("a launcher's rows = %+v, want their own alone", own.Rows)
		}
		if len(own.Totals) != 1 || own.Totals[0].Runs != 1 || own.Totals[0].CountedUSD != 1.00 {
			t.Errorf("a launcher's total = %+v, want their own run and nobody else's money", own.Totals)
		}
		raw, _ := json.Marshal(own)
		if strings.Contains(string(raw), "sam@example.com") {
			t.Errorf("another user's identity reached a launcher's private view: %s", raw)
		}
		human, _, err := execute(t, "runs", "stats")
		if err != nil || !strings.Contains(human, "your own runs only") || strings.Contains(human, "sam@example.com") {
			t.Errorf("the private table = %q, %v", human, err)
		}
		all := runsStats(t, "--all-users")
		if all.View != "all" || all.Operator || len(all.Rows) != 2 {
			t.Fatalf("--all-users = %+v", all)
		}
		humanAll, _, err := execute(t, "runs", "stats", "--all-users")
		if err != nil || !strings.Contains(humanAll, "every user of this installation") {
			t.Errorf("--all-users table = %q, %v: the full table names whose numbers it shows", humanAll, err)
		}
	})

	t.Run("the caller's own identity is the table's own domain", func(t *testing.T) {
		f := newCloudFixture(t)
		// The config's identity carries an invisible character (a paste) and a
		// long tail, and the run recorded the same value: `you` and the row
		// key are one text, sanitised and capped -- a consumer matches `you`
		// against rows[].user, and an empty table would be the private view
		// showing the caller nobody.
		raw := "dana@example.com​" + strings.Repeat("z", 200)
		userStatsPlace(t, f, raw, []string{"user:" + raw}, nil)
		seedUserRun(t, f, userStatsID(day, 0), raw, userRec(runstore.StatusSucceeded, 1.00, runstore.BasisAPIList))
		seedUserRun(t, f, userStatsID(day, 1), "sam@example.com", userRec(runstore.StatusSucceeded, 2.00, runstore.BasisAPIList))

		doc := runsStats(t)
		if len(doc.Rows) != 1 {
			t.Fatalf("the caller's own view = %+v, want their own row alone", doc.Rows)
		}
		if doc.You != doc.Rows[0].User {
			t.Errorf("you = %q, the row key = %q: the two are not in the same domain, so no "+
				"consumer can match them", doc.You, doc.Rows[0].User)
		}
		if !strings.HasPrefix(doc.You, "dana@example.com") {
			t.Errorf("you = %q, want the printable prefix to survive", doc.You)
		}
		if n := len([]rune(doc.You)); n > userstats.MaxIdentityRunes {
			t.Errorf("you is %d runes, want at most %d", n, userstats.MaxIdentityRunes)
		}
		for _, bad := range []string{"​", "\x1b", "\n"} {
			if strings.Contains(doc.You, bad) {
				t.Errorf("you = %q carries %q", doc.You, bad)
			}
		}
		// The table's note names the same text, from the same field.
		human, _, err := execute(t, "runs", "stats")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(human, "you are "+doc.You) {
			t.Errorf("the note does not name the identity the document states:\n%s", human)
		}
		if strings.Contains(human, "​") {
			t.Errorf("the table carries the invisible character:\n%q", human)
		}
	})

	t.Run("a key two people share is not a person's own", func(t *testing.T) {
		f := newCloudFixture(t)
		// Two launchers whose identities differ by one character the
		// sanitising drops, so the table keys them alike: this is the
		// collision the cap is documented for, reached the other way. The
		// caller's private view is the runs their own identity recorded --
		// what runs ls --mine means by "your own" -- so the other person's
		// run is not in it, however the row they share is keyed.
		userStatsPlace(t, f, "alice@example.com", []string{"user:alice@example.com"}, nil)
		twin := "ali​ce@example.com"
		seedUserRun(t, f, userStatsID(day, 0), "alice@example.com", userRec(runstore.StatusSucceeded, 1.00, runstore.BasisAPIList))
		seedUserRun(t, f, userStatsID(day, 1), twin, userRec(runstore.StatusSucceeded, 2.00, runstore.BasisAPIList))

		own := runsStats(t)
		if own.View != "own" {
			t.Fatalf("view = %q, want the private one", own.View)
		}
		if len(own.Rows) != 1 || own.Rows[0].User != "alice@example.com" {
			t.Fatalf("the caller's own view = %+v, want the one key the two share", own.Rows)
		}
		if got := own.Rows[0]; got.Runs != 1 || got.CountedUSD != 1.00 {
			t.Errorf("the caller's own row = %+v, want their own run alone: %q sanitises to the "+
				"same key as %q, and a key is not a person", got, safetext.Field(twin), "alice@example.com")
		}
		if own.You != own.Rows[0].User {
			t.Errorf("you = %q, the row key = %q", own.You, own.Rows[0].User)
		}
		// The full table still shows the merged row, under the key the two
		// sanitise to -- a collision the table cannot split, and says so about.
		all := runsStats(t, "--all-users")
		if len(all.Rows) != 1 || all.Rows[0].Runs != 2 || all.Rows[0].CountedUSD != 3.00 {
			t.Errorf("--all-users rows = %+v, want both runs under the one key", all.Rows)
		}
	})

	t.Run("an identity that is no key of its own is shown no row", func(t *testing.T) {
		// The unattributed row is keyed by the word 'unknown', and a caller
		// whose own identity sanitises to that same word is not that row's
		// owner: the row is everybody's runs that recorded no launcher, and
		// showing it as the caller's own would credit them with other people's
		// runs. Their own view is empty instead -- and their identity is
		// nothing the CLI can tell, so `you` says so rather than naming them.
		for _, identity := range []string{"​", userstats.UnknownUser} {
			t.Run(strconv.Quote(identity), func(t *testing.T) {
				f := newCloudFixture(t)
				userStatsPlace(t, f, identity, nil, nil)
				seed(t, f)
				// One run nobody recorded a launcher for: without the guard
				// this is the row the caller's own view would show them.
				seedUserRun(t, f, userStatsID(day, 2), "", userRec(runstore.StatusSucceeded, 3.00, runstore.BasisAPIList))

				own := runsStats(t)
				if own.View != "own" {
					t.Fatalf("view = %q, want the private one", own.View)
				}
				if len(own.Rows) != 0 || len(own.Totals) != 0 {
					t.Fatalf("the caller's own view = %+v / %+v, want nothing: a row keyed %q is "+
						"everyone's runs that recorded no launcher, not this caller's own",
						own.Rows, own.Totals, userstats.UnknownUser)
				}
				// An empty view prints [] like every other list in the document,
				// and `you` says what the CLI could tell: nothing at all for an
				// identity with no printable text in it, and the identity's own
				// text for one that reads as the unattributed key.
				if own.Rows == nil {
					t.Errorf("document = %+v, want an empty row list, not null", own)
				}
				wantYou := ""
				if identity == userstats.UnknownUser {
					wantYou = userstats.UnknownUser
				}
				if own.You != wantYou {
					t.Errorf("you = %q, want %q", own.You, wantYou)
				}
				human, _, err := execute(t, "runs", "stats")
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(human, "no run in this window was started by you") {
					t.Errorf("the empty private table = %q, want it said plainly", human)
				}
				if identity == userstats.UnknownUser && !strings.Contains(human, "is not only yours") {
					t.Errorf("the table = %q, want the caller told that the row keyed %q is not "+
						"theirs alone", human, userstats.UnknownUser)
				}
				// The opt-in still shows everyone, the unattributed row included:
				// the guard is on whose row the own view claims, not on the table.
				all := runsStats(t, "--all-users")
				if len(all.Rows) != 3 {
					t.Fatalf("--all-users rows = %+v, want every user's, the unattributed one included", all.Rows)
				}
				if got := userStatsRow(t, all, userstats.UnknownUser).CountedUSD; got != 3.00 {
					t.Errorf("the unattributed row = %v, want the unrecorded launch's own money", got)
				}
			})
		}
	})

	t.Run("lists that do not place the caller stay private", func(t *testing.T) {
		f := newCloudFixture(t)
		// The installation names neither list: every installation from before
		// the 0.7.0 hardening looks like this, and the caller is not an
		// operator of it.
		userStatsPlace(t, f, "dana@example.com", nil, nil)
		seed(t, f)
		doc := runsStats(t)
		if doc.View != "own" || doc.Operator || len(doc.Rows) != 1 {
			t.Fatalf("an unplaced caller's view = %+v, want the private one", doc)
		}
	})

	t.Run("an identity that cannot be told is refused, not shown everyone", func(t *testing.T) {
		f := newCloudFixture(t)
		userStatsPlace(t, f, "", nil, nil)
		seed(t, f)
		// The identity is neither the config's nor git's: an empty GIT_DIR is
		// not a repository, so git config user.email cannot answer either.
		testutil.IsolateGit(t)
		t.Setenv("GIT_DIR", t.TempDir())
		out, _, err := execute(t, "runs", "stats", "--json")
		if err == nil {
			t.Fatalf("an identity that cannot be told was shown a table: %s", out)
		}
		if !strings.Contains(err.Error(), "--all-users") {
			t.Errorf("the refusal = %v, want it to name the way to the full table", err)
		}
		// The explicit opt-in still works: it is the caller's own request for
		// the whole table, and it never needed to know who they are.
		doc := runsStats(t, "--all-users")
		if doc.View != "all" || doc.You != "" || doc.Operator || len(doc.Rows) != 2 {
			t.Fatalf("--all-users with no identity = %+v", doc)
		}
	})
}

// TestRunsStatsOwnViewShowsNothingForACallerWithNoKeyOfItsOwn is the guard's
// other half (runs_stats.go, the own-view filter's `own == "" ||`), and the
// one the recorded-text comparison cannot reach on its own. A caller whose
// identity is no key of its own -- nothing printable in it, or the word
// 'unknown' itself -- is shown no row, and that holds even when runs recorded
// the caller's exact text: runs ls --mine's comparison keeps those runs (the
// recorded text is theirs), and the row they land in is keyed 'unknown' either
// way, the key everybody's unattributed runs are grouped under. The private
// table would then print that row under "your own runs only" while the note
// beside it says no row below is shown as yours.
//
// So the fixture here records runs by the caller's own text, which the
// sub-test in TestRunsStatsVisibilityIsTheRuling deliberately does not: with
// no such run, deleting `own == "" ||` leaves the package green, which is the
// gap this closes.
func TestRunsStatsOwnViewShowsNothingForACallerWithNoKeyOfItsOwn(t *testing.T) {
	day := time.Now().UTC()
	for _, tc := range []struct {
		name, identity string
	}{
		{"nothing printable in the identity", "​"},
		{"the identity is the unattributed key's own word", userstats.UnknownUser},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCloudFixture(t)
			userStatsPlace(t, f, tc.identity, nil, nil)
			// Two runs recorded by the caller's own text, one recorded by
			// nobody, and one by somebody else. All four are keyed 'unknown' in
			// the first two cases' tables, which is why the private view must
			// not be read off the row they share.
			seedUserRun(t, f, userStatsID(day, 0), tc.identity, userRec(runstore.StatusSucceeded, 7.00, runstore.BasisAPIList))
			seedUserRun(t, f, userStatsID(day, 1), tc.identity, userRec(runstore.StatusFailed, 2.00, runstore.BasisAPIList))
			seedUserRun(t, f, userStatsID(day, 2), "", userRec(runstore.StatusSucceeded, 3.00, runstore.BasisAPIList))
			seedUserRun(t, f, userStatsID(day, 3), "dana@example.com", userRec(runstore.StatusSucceeded, 1.00, runstore.BasisAPIList))

			own := runsStats(t)
			if own.View != "own" {
				t.Fatalf("view = %q, want the private one", own.View)
			}
			if len(own.Rows) != 0 || len(own.Totals) != 0 {
				t.Fatalf("the caller's own view = %+v / %+v, want nothing: their own runs are keyed "+
					"%q, the key every run that recorded no launcher is grouped under, and that row "+
					"is not a person's", own.Rows, own.Totals, userstats.UnknownUser)
			}
			human, _, err := execute(t, "runs", "stats")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(human, "no run in this window was started by you") {
				t.Errorf("the private table = %q, want it said plainly rather than shown as a table", human)
			}
			if strings.Contains(human, "USER") {
				t.Errorf("the private table = %q, want no table at all: its one row would be keyed %q "+
					"and that row is nobody's own", human, userstats.UnknownUser)
			}
			if tc.identity == userstats.UnknownUser && !strings.Contains(human, "no row below is shown as yours") {
				t.Errorf("the private table = %q, want the note that says the row keyed %q is not "+
					"shown as theirs -- the words the mutant above contradicts", human, userstats.UnknownUser)
			}
			// The guard is on whose row the own view claims, not on the table a
			// caller asks for: --all-users still shows every run, the caller's
			// own text among them.
			all := runsStats(t, "--all-users")
			if len(all.Rows) != 2 {
				t.Fatalf("--all-users rows = %+v, want the %q row and dana's", all.Rows, userstats.UnknownUser)
			}
			if got := userStatsRow(t, all, userstats.UnknownUser); got.Runs != 3 || got.CountedUSD != 12.00 {
				t.Errorf("the %q row = %+v, want every run keyed under it: the two this caller "+
					"recorded and the one nobody did", userstats.UnknownUser, got)
			}
		})
	}
}

// TestRunsStatsHelpStatesThePrivateViewsThreeWays is the help's half of the
// ruling. The code has three states here, and the help a reader gets first
// must have them too: an identity the CLI cannot resolve at all -- the local
// config's user: empty and git config user.email answering nothing -- is
// refused (no document at all, an error naming --all-users), an identity with
// nothing printable left in it is shown an empty table, and so is one whose
// own text reads as the unattributed key. Folding the refusal in with the
// empty table is the mistake this pins: a reader told "no row at all" about a
// case the command will not run for them has been told the wrong thing about
// the one output they read first. The contract's paragraph on the same three
// states is pinned by
// TestDocsDesktopConsoleContractStatesTheRunsStatsOwnViewRefusal, in the one
// file the console boundary lets read the contract.
func TestRunsStatsHelpStatesThePrivateViewsThreeWays(t *testing.T) {
	help := newRunsStatsCmd().Long
	for _, want := range []string{
		"the local config names nobody",
		"is refused, never quietly widened",
		"names --all-users",
		"nothing printable is left of it",
		"the table is empty",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("the help does not say %q:\n%s", want, help)
		}
	}

	// The code's own answer to the first of the three, so the words above are
	// read against it rather than against each other.
	f := newCloudFixture(t)
	userStatsPlace(t, f, "", nil, nil)
	testutil.IsolateGit(t)
	t.Setenv("GIT_DIR", t.TempDir())
	out, _, err := execute(t, "runs", "stats", "--json")
	if err == nil || !strings.Contains(err.Error(), "--all-users") {
		t.Fatalf("no identity: err = %v, want the refusal the help describes (out: %s)", err, out)
	}
}

// TestRunsStatsReadIsBoundedAndAdmitsIt is the read rule: the window is read
// newest first up to --max-runs, and a window holding more says so in both
// outputs rather than publishing figures over a part of itself quietly.
func TestRunsStatsReadIsBoundedAndAdmitsIt(t *testing.T) {
	f := newCloudFixture(t)
	userStatsPlace(t, f, "someone@example.com", nil, []string{"user:someone@example.com"})
	day := time.Now().UTC()
	// Three runs and a read of two: only the two newest are read, so the
	// oldest run's money is in no figure -- and the output says the read was
	// cut.
	for i, who := range []string{"dana@example.com", "sam@example.com", "pat@example.com"} {
		seedUserRun(t, f, userStatsID(day, i), who, userRec(runstore.StatusSucceeded, 1.00, runstore.BasisAPIList))
	}
	doc := runsStats(t, "--max-runs", "2")
	if !doc.Truncated || doc.MaxRuns != 2 {
		t.Fatalf("truncated = %v, max_runs = %d, want true and 2", doc.Truncated, doc.MaxRuns)
	}
	if len(doc.Rows) != 2 || doc.Totals[0].Runs != 2 {
		t.Fatalf("rows = %+v over %d runs, want the newest two", doc.Rows, doc.Totals[0].Runs)
	}
	// The newest two, not the first two the listing happened to give: the
	// oldest run's launcher is in no row at all.
	for _, r := range doc.Rows {
		if r.User == "dana@example.com" {
			t.Errorf("the oldest run's launcher is in the read: %+v", doc.Rows)
		}
	}
	if userStatsRow(t, doc, "pat@example.com").Runs != 1 || userStatsRow(t, doc, "sam@example.com").Runs != 1 {
		t.Errorf("rows = %+v, want the two newest runs", doc.Rows)
	}
	human, _, err := execute(t, "runs", "stats", "--max-runs", "2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human, "truncated") {
		t.Errorf("the table does not admit the truncation:\n%s", human)
	}
	// The notes say what the read left out, in words, beside the flag.
	notes := strings.Join(doc.Notes, "\n")
	if !strings.Contains(notes, "holds more runs than") || !strings.Contains(notes, "no figure below is over more than its newest 2") {
		t.Errorf("the document does not admit the truncation: %+v", doc.Notes)
	}
	// A window that fits is not truncated, and says nothing about one.
	full := runsStats(t)
	if full.Truncated || len(full.Rows) != 3 {
		t.Fatalf("an untruncated read = %+v", full)
	}
}

// TestRunsStatsMaxRunsBoundaryIsExact is the bound's own edge: truncation is
// "the window holds MORE than --max-runs", not "as many as". A window holding
// exactly the bound is read whole, so a reader is told nothing was left out --
// the flag, the notes and the window line all stay quiet -- and one run more
// cuts the read to the newest max, which is the oldest run dropped.
func TestRunsStatsMaxRunsBoundaryIsExact(t *testing.T) {
	f := newCloudFixture(t)
	userStatsPlace(t, f, "someone@example.com", nil, []string{"user:someone@example.com"})
	day := time.Now().UTC()
	for i, who := range []string{"dana@example.com", "sam@example.com", "pat@example.com"} {
		seedUserRun(t, f, userStatsID(day, i), who, userRec(runstore.StatusSucceeded, 1.00, runstore.BasisAPIList))
	}

	exact := runsStats(t, "--max-runs", "3")
	if exact.Truncated || exact.MaxRuns != 3 {
		t.Fatalf("a window holding exactly --max-runs runs: truncated = %v over max_runs = %d, "+
			"want false and 3 -- the read left nothing out", exact.Truncated, exact.MaxRuns)
	}
	if len(exact.Rows) != 3 || len(exact.Totals) != 1 || exact.Totals[0].Runs != 3 {
		t.Errorf("rows = %+v / totals = %+v, want all three runs", exact.Rows, exact.Totals)
	}
	if notes := strings.Join(exact.Notes, "\n"); strings.Contains(notes, "holds more runs than") {
		t.Errorf("an untruncated read's notes admit a cut: %+v", exact.Notes)
	}
	human, _, err := execute(t, "runs", "stats", "--max-runs", "3")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(human, "truncated") {
		t.Errorf("the table of an exact read says it was cut:\n%s", human)
	}

	// One run past the bound: cut to the newest three, so the oldest run's
	// launcher is in no figure and the newest one's is.
	seedUserRun(t, f, userStatsID(day, 3), "kim@example.com", userRec(runstore.StatusSucceeded, 4.00, runstore.BasisAPIList))
	over := runsStats(t, "--max-runs", "3")
	if !over.Truncated {
		t.Fatalf("a window holding one run more than --max-runs = %+v, want it truncated", over)
	}
	if len(over.Totals) != 1 || over.Totals[0].Runs != 3 {
		t.Errorf("the cut read is over %+v, want three runs", over.Totals)
	}
	if got := userStatsRow(t, over, "kim@example.com").CountedUSD; got != 4.00 {
		t.Errorf("the newest run's launcher has %v, want 4.00: the read is over the newest runs", got)
	}
	for _, r := range over.Rows {
		if r.User == "dana@example.com" {
			t.Errorf("the oldest run is in a read cut to three: %+v", over.Rows)
		}
	}
}

// TestRunsStatsCountsWhatNeverStarted is the read's other edge: a run
// directory with neither a launch nor a record never started, so it is in no
// figure -- and the count of them is printed rather than left as a silent
// difference between the bucket's listing and the table.
func TestRunsStatsCountsWhatNeverStarted(t *testing.T) {
	f := newCloudFixture(t)
	userStatsPlace(t, f, "someone@example.com", nil, []string{"user:someone@example.com"})
	day := time.Now().UTC()
	seedUserRun(t, f, userStatsID(day, 0), "dana@example.com", userRec(runstore.StatusSucceeded, 1.00, runstore.BasisAPIList))
	// A run that was claimed and never launched: it has a task.json and
	// nothing else.
	id := userStatsID(day, 1)
	if err := runstore.Open(statsBucket(t, f), appSlug, id).CreateTask(context.Background(), &task.Spec{
		Version: 1, RunID: id, Repo: "acme/app", Ref: "main", Workflow: "web",
		Task: "rewrite the billing pipeline", RequestedBy: "dana@example.com",
	}); err != nil {
		t.Fatal(err)
	}

	doc := runsStats(t)
	if doc.Unlaunched != 1 {
		t.Fatalf("unlaunched = %d, want 1", doc.Unlaunched)
	}
	if len(doc.Rows) != 1 || doc.Rows[0].Runs != 1 {
		t.Fatalf("rows = %+v, want one run: a run that never started is in no figure", doc.Rows)
	}
	human, _, err := execute(t, "runs", "stats")
	if err != nil || !strings.Contains(human, "never launched") {
		t.Errorf("the table = %q, %v: it must say what it left out", human, err)
	}
}

// TestRunsStatsUnreadableRecordIsUnknownNotZero is the same rule for a run
// that did start: a launched run whose result.json is missing or corrupt is
// counted, under the launcher its launch recorded, with its outcome and its
// money unknown -- never read as a run that cost nothing.
func TestRunsStatsUnreadableRecordIsUnknownNotZero(t *testing.T) {
	f := newCloudFixture(t)
	userStatsPlace(t, f, "someone@example.com", nil, []string{"user:someone@example.com"})
	day := time.Now().UTC()
	// Launched, recorded nothing: no result.json at all.
	seedUserRun(t, f, userStatsID(day, 0), "dana@example.com", nil)
	seedUserRun(t, f, userStatsID(day, 1), "dana@example.com", userRec(runstore.StatusSucceeded, 2.00, runstore.BasisAPIList))
	// A record nobody can decode: this run's problem, not the window's.
	if err := runstore.Open(statsBucket(t, f), appSlug, userStatsID(day, 2)).
		PutFile(context.Background(), "result.json", []byte(`{"version":1,"run_id":`), "application/json"); err != nil {
		t.Fatal(err)
	}
	seedUserRun(t, f, userStatsID(day, 2), "dana@example.com", nil)

	doc := runsStats(t)
	dana := userStatsRow(t, doc, "dana@example.com")
	if dana.Runs != 3 {
		t.Fatalf("dana = %+v, want all three runs counted", dana)
	}
	if dana.NoRecord != 2 || dana.Succeeded != 1 {
		t.Errorf("dana = %+v, want 2 with no readable record and 1 succeeded", dana)
	}
	if dana.CountedUSD != 2.00 || dana.CountedRuns != 1 || dana.AvgCountedUSD != 2.00 {
		t.Errorf("dana = %+v: an unreadable record is not a run that cost $0.00", dana)
	}
}

// TestRunsStatsUnreadableLaunchIsUnknownNotUnlaunched is the other half of that
// rule, and the case the read's own count got wrong: a run whose launch.json is
// there and unreadable, with no result.json beside it, DID start -- so it is
// counted among the runs under 'unknown' (a launcher nobody can read is not a
// launcher nobody recorded), and it is not in the count of runs that never
// started. That count is what the table's "never launched" note rests on, and
// runs ls reports the same object as an error row: the two must not disagree
// about whether the run happened, or the by-user figures silently drop it.
//
// Mutation (verified, run and restored): drop the `!o.corrupt` guard from the
// unlaunched arm in readUserStatsRuns -- this fails, reporting the run as never
// launched and leaving it out of every row.
func TestRunsStatsUnreadableLaunchIsUnknownNotUnlaunched(t *testing.T) {
	f := newCloudFixture(t)
	userStatsPlace(t, f, "someone@example.com", nil, []string{"user:someone@example.com"})
	day := time.Now().UTC()
	seedUserRun(t, f, userStatsID(day, 0), "dana@example.com", userRec(runstore.StatusSucceeded, 1.00, runstore.BasisAPIList))
	// Launched, with a launch record nobody can decode and no result.json at
	// all: nothing but the corrupt object says this run ever happened.
	if err := runstore.Open(statsBucket(t, f), appSlug, userStatsID(day, 1)).
		PutFile(context.Background(), "launch.json", []byte(`{"version":1,"run_id":`), "application/json"); err != nil {
		t.Fatal(err)
	}

	doc := runsStats(t)
	if doc.Unlaunched != 0 {
		t.Fatalf("unlaunched = %d, want 0: an unreadable launch.json is a run that launched, not one that never started", doc.Unlaunched)
	}
	unknown := userStatsRow(t, doc, userstats.UnknownUser)
	if unknown.Runs != 1 || unknown.NoRecord != 1 {
		t.Errorf("unknown = %+v, want the run counted under %q with no readable record", unknown, userstats.UnknownUser)
	}
	dana := userStatsRow(t, doc, "dana@example.com")
	if dana.Runs != 1 || dana.CountedUSD != 1.00 {
		t.Errorf("dana = %+v, want her own run and its money: the unreadable run is not hers to carry", dana)
	}
}

// TestRunsStatsHostileLauncherIsSanitised is the untrusted-text rule: the
// launcher is text a launcher wrote, so it is made one printable line and
// size-capped before it reaches a row, a column or a key.
func TestRunsStatsHostileLauncherIsSanitised(t *testing.T) {
	f := newCloudFixture(t)
	userStatsPlace(t, f, "someone@example.com", nil, []string{"user:someone@example.com"})
	day := time.Now().UTC()
	hostile := "dana@example.com\t\x1b[2J\u202e" + strings.Repeat("z", 5000)
	seedUserRun(t, f, userStatsID(day, 0), hostile, userRec(runstore.StatusSucceeded, 1.00, runstore.BasisAPIList))

	doc := runsStats(t)
	if len(doc.Rows) != 1 {
		t.Fatalf("rows = %+v", doc.Rows)
	}
	user := doc.Rows[0].User
	if n := len([]rune(user)); n > userstats.MaxIdentityRunes {
		t.Errorf("the key is %d runes, want at most %d", n, userstats.MaxIdentityRunes)
	}
	if !strings.HasPrefix(user, "dana@example.com") {
		t.Errorf("the key %q, want the printable prefix to survive", user)
	}
	for _, bad := range []string{"\x1b", "\t", "\u202e", "\n"} {
		if strings.Contains(user, bad) {
			t.Errorf("the key %q carries %q", user, bad)
		}
	}
	human, _, err := execute(t, "runs", "stats")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(human, "\x1b") || strings.Contains(human, "\u202e") {
		t.Errorf("the table carries what the launcher wrote:\n%q", human)
	}
	for _, line := range strings.Split(human, "\n") {
		if strings.HasPrefix(line, "dana") && len([]rune(line)) > maxNameCellRunes+256 {
			t.Errorf("a table row is %d runes long, want the cell clipped:\n%s", len([]rune(line)), line)
		}
	}
	// The identity is display text and nothing else: it is compared against no
	// access list, and the sanitising is what makes it safe to print.
	if safetext.Field(hostile) == hostile {
		t.Fatal("the fixture's value is not one safetext.Field had to change")
	}
}

// TestRunsStatsIdentitiesPastTheCapAreOneRow is the cap's own consequence,
// stated in the help: two identities that differ only past the cap are one
// row, because the cap is what the table groups by and shows.
func TestRunsStatsIdentitiesPastTheCapAreOneRow(t *testing.T) {
	f := newCloudFixture(t)
	userStatsPlace(t, f, "someone@example.com", nil, []string{"user:someone@example.com"})
	day := time.Now().UTC()
	// Long enough to be cut by the cap and short enough not to be cut by
	// safetext.Field's own: the two differ past the cap alone. The length is
	// written out rather than derived from the cap, so lowering the cap keeps
	// the two apart and raising it does not quietly make them the same value.
	base := strings.Repeat("a", 200)
	seedUserRun(t, f, userStatsID(day, 0), base+"-one", userRec(runstore.StatusSucceeded, 1.00, runstore.BasisAPIList))
	seedUserRun(t, f, userStatsID(day, 1), base+"-two", userRec(runstore.StatusSucceeded, 2.00, runstore.BasisAPIList))

	doc := runsStats(t)
	if len(doc.Rows) != 1 {
		t.Fatalf("rows = %+v, want one: the identities differ only past the cap", doc.Rows)
	}
	if doc.Rows[0].Runs != 2 || doc.Rows[0].CountedUSD != 3.00 {
		t.Errorf("the row = %+v, want both runs", doc.Rows[0])
	}
}

// TestWriteUserStatsNamesEachCurrencyWhenSeveral is the table's half of the
// currency rule. No record carries a currency today -- result.json has no such
// field, so the CLI always aggregates in USD -- but the aggregation and the
// document both carry one, and the day a second currency appears the table
// must not print two rows of different units under one undivided total. The
// renderer is held to that here, off a document built for the case, rather
// than left as a branch nothing reaches.
func TestWriteUserStatsNamesEachCurrencyWhenSeveral(t *testing.T) {
	doc := userStatsDoc{
		Project: "aurora", By: userStatsByUser, View: userStatsViewAll,
		You: "someone@example.com", Operator: true, Since: "2026-10-11", Until: "2026-10-18",
		MaxRuns: userStatsMaxRuns,
		Rows: []userstats.Row{
			{User: "dana@example.com", Currency: "EUR", Counts: userstats.Counts{Runs: 1, Succeeded: 1},
				CountedUSD: 90, CountedRuns: 1, AvgCountedUSD: 90},
			{User: "dana@example.com", Currency: userstats.CurrencyUSD, Counts: userstats.Counts{Runs: 1, Succeeded: 1},
				CountedUSD: 10, CountedRuns: 1, AvgCountedUSD: 10},
		},
		Totals: []userstats.Row{
			{Currency: "EUR", Counts: userstats.Counts{Runs: 1, Succeeded: 1}, CountedUSD: 90, CountedRuns: 1, AvgCountedUSD: 90},
			{Currency: userstats.CurrencyUSD, Counts: userstats.Counts{Runs: 1, Succeeded: 1}, CountedUSD: 10, CountedRuns: 1, AvgCountedUSD: 10},
		},
	}
	var b strings.Builder
	if err := writeUserStats(&b, doc, "7d", false); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	// The column appears, and each row says which unit its dollars are in.
	if !strings.Contains(out, "CUR") || !strings.Contains(out, "EUR") {
		t.Errorf("the table shows no currency column:\n%s", out)
	}
	// The totals are named per currency: "TOTAL" alone would be the one line a
	// reader could take for a sum across units.
	for _, want := range []string{"TOTAL EUR", "TOTAL USD"} {
		if !strings.Contains(out, want) {
			t.Errorf("the table does not carry %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\tTOTAL\t") || strings.Contains(strings.ReplaceAll(out, " ", ""), "TOTAL$") {
		t.Errorf("a total is unnamed:\n%s", out)
	}

	// One currency, one column: the always-same word is not a fact a reader
	// needs.
	single := userStatsDoc{Project: "aurora", By: userStatsByUser, View: userStatsViewOwn,
		You: "dana@example.com", Since: "2026-10-11", Until: "2026-10-18", MaxRuns: userStatsMaxRuns,
		Rows:   []userstats.Row{{User: "dana@example.com", Currency: userstats.CurrencyUSD, Counts: userstats.Counts{Runs: 1, Succeeded: 1}, CountedUSD: 10, CountedRuns: 1, AvgCountedUSD: 10}},
		Totals: []userstats.Row{{Currency: userstats.CurrencyUSD, Counts: userstats.Counts{Runs: 1, Succeeded: 1}, CountedUSD: 10, CountedRuns: 1, AvgCountedUSD: 10}},
	}
	b.Reset()
	if err := writeUserStats(&b, single, "7d", false); err != nil {
		t.Fatal(err)
	}
	if out := b.String(); !strings.Contains(out, "USER") || strings.Contains(out, "CUR") || strings.Contains(out, "TOTAL USD") {
		t.Errorf("a one-currency table = \n%s", out)
	}
}

// userStatsTable is a rendered table's header and its rows, each split into
// cells the way the tabwriter's own padding delimits them. The command's
// table pads every column with at least two spaces, and no value it prints
// holds a run of two.
func userStatsTable(t *testing.T, out string) (header []string, rows [][]string) {
	t.Helper()
	cell := func(line string) []string {
		return regexp.MustCompile(`  +`).Split(strings.TrimRight(line, " "), -1)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if header == nil {
			if strings.HasPrefix(line, "USER") {
				header = cell(line)
			}
			continue
		}
		rows = append(rows, cell(line))
	}
	if header == nil {
		t.Fatalf("no table in:\n%s", out)
	}
	return header, rows
}

// userStatsCellsPinned holds every cell of a rendered table to the value it
// must carry, by its own column's position. The table is this command's
// default output, and a "contains" assertion cannot hold it: swapping two
// columns in the row format leaves every figure present in the output and
// every per-figure assertion green, with the numbers under the wrong
// headings. So both the header and each row are compared cell by cell, and
// each row of the fixture carries a different value in every column.
func userStatsCellsPinned(t *testing.T, out string, wantHeader []string, wantRows [][]string) {
	t.Helper()
	header, rows := userStatsTable(t, out)
	if !slices.Equal(header, wantHeader) {
		t.Fatalf("the table's columns = %q, want %q", header, wantHeader)
	}
	if len(rows) != len(wantRows) {
		t.Fatalf("the table has %d rows, want %d:\n%s", len(rows), len(wantRows), out)
	}
	for i, want := range wantRows {
		if len(rows[i]) != len(header) {
			t.Fatalf("row %d = %q has %d cells, the header has %d:\n%s", i, rows[i], len(rows[i]), len(header), out)
		}
		for j := range want {
			if rows[i][j] != want[j] {
				t.Errorf("row %d, column %q = %q, want %q (the whole row: %q)",
					i, header[j], rows[i][j], want[j], rows[i])
			}
		}
	}
}

// TestWriteUserStatsPinsEveryColumn is the table's own contract: each figure
// sits under its own heading. TestWriteUserStatsNamesEachCurrencyWhenSeveral
// holds the currency column and the totals' names, and every other test reads
// the table with a "contains", so until this one nothing pinned the mapping
// from columns to values -- SUCCEEDED and FAILED, or COUNTED $ and NOTIONAL $,
// could have been swapped in the row format with every test green.
func TestWriteUserStatsPinsEveryColumn(t *testing.T) {
	doc := userStatsDoc{
		Project: "aurora", By: userStatsByUser, View: userStatsViewAll,
		You: "someone@example.com", Operator: true, Since: "2026-10-11", Until: "2026-10-18",
		MaxRuns: userStatsMaxRuns,
		Rows: []userstats.Row{
			// Every column of this row is a different figure, and its counts add
			// up (1+2+3+4+5+6 = 21) with $8.89 over 7 counted runs at $1.27 each.
			{User: "dana@example.com", Currency: userstats.CurrencyUSD,
				Counts:     userstats.Counts{Runs: 21, Succeeded: 1, Failed: 2, Halted: 3, InfraError: 4, Other: 5, NoRecord: 6},
				CountedUSD: 8.89, NotionalUSD: 2.22, CountedRuns: 7, AvgCountedUSD: 1.27},
			{User: "sam@example.com", Currency: userstats.CurrencyUSD,
				Counts:     userstats.Counts{Runs: 10, Succeeded: 0, Failed: 1, Halted: 2, InfraError: 3, Other: 4, NoRecord: 0},
				CountedUSD: 1.00, NotionalUSD: 0.50, CountedRuns: 2, AvgCountedUSD: 0.50},
		},
		Totals: []userstats.Row{
			{Currency: userstats.CurrencyUSD,
				Counts:     userstats.Counts{Runs: 31, Succeeded: 1, Failed: 3, Halted: 5, InfraError: 7, Other: 9, NoRecord: 6},
				CountedUSD: 9.89, NotionalUSD: 2.72, CountedRuns: 9, AvgCountedUSD: 1.10},
		},
	}
	var b strings.Builder
	if err := writeUserStats(&b, doc, "7d", false); err != nil {
		t.Fatal(err)
	}
	userStatsCellsPinned(t, b.String(),
		[]string{"USER", "RUNS", "SUCCEEDED", "FAILED", "HALTED", "INFRA ERROR", "OTHER", "NO RECORD",
			"COUNTED $", "NOTIONAL $", "COUNTED RUNS", "AVG $/RUN"},
		[][]string{
			{"dana@example.com", "21", "1", "2", "3", "4", "5", "6", "$8.89", "$2.22", "7", "$1.27"},
			{"sam@example.com", "10", "0", "1", "2", "3", "4", "0", "$1.00", "$0.50", "2", "$0.50"},
			{"TOTAL", "31", "1", "3", "5", "7", "9", "6", "$9.89", "$2.72", "9", "$1.10"},
		})
}

// TestWriteUserStatsAverageIsDashWithoutACountedRun pins the one cell the
// document cannot carry: a group with runs but no run that settled a
// real-dollar cost has no average, and the table says "-" rather than "$0.00
// a run" -- the reading the help, the field's own comment and the contract
// all call unavailable rather than a figure.
func TestWriteUserStatsAverageIsDashWithoutACountedRun(t *testing.T) {
	head := []string{"USER", "RUNS", "SUCCEEDED", "FAILED", "HALTED", "INFRA ERROR", "OTHER", "NO RECORD",
		"COUNTED $", "NOTIONAL $", "COUNTED RUNS", "AVG $/RUN"}
	notional := userstats.Row{User: "ci@example.com", Currency: userstats.CurrencyUSD,
		Counts:      userstats.Counts{Runs: 2, Succeeded: 2},
		NotionalUSD: 5.00, CountedRuns: 0, AvgCountedUSD: 0}
	counted := userstats.Row{User: "dana@example.com", Currency: userstats.CurrencyUSD,
		Counts:     userstats.Counts{Runs: 1, Succeeded: 1},
		CountedUSD: 4.00, CountedRuns: 2, AvgCountedUSD: 2.00}
	doc := userStatsDoc{
		Project: "aurora", By: userStatsByUser, View: userStatsViewAll,
		You: "someone@example.com", Operator: true, Since: "2026-10-11", Until: "2026-10-18",
		MaxRuns: userStatsMaxRuns,
		Rows:    []userstats.Row{notional, counted},
		Totals: []userstats.Row{{Currency: userstats.CurrencyUSD,
			Counts:     userstats.Counts{Runs: 3, Succeeded: 3},
			CountedUSD: 4.00, NotionalUSD: 5.00, CountedRuns: 2, AvgCountedUSD: 2.00}},
	}
	var b strings.Builder
	if err := writeUserStats(&b, doc, "7d", false); err != nil {
		t.Fatal(err)
	}
	userStatsCellsPinned(t, b.String(), head, [][]string{
		// The notional run cost the plan's limits, not a bill: no counted run,
		// no average to state.
		{"ci@example.com", "2", "2", "0", "0", "0", "0", "0", "$0.00", "$5.00", "0", "-"},
		{"dana@example.com", "1", "1", "0", "0", "0", "0", "0", "$4.00", "$0.00", "2", "$2.00"},
		// The total's average is over its own counted runs, so it is a figure
		// even though one of the rows above has none.
		{"TOTAL", "3", "3", "0", "0", "0", "0", "0", "$4.00", "$5.00", "2", "$2.00"},
	})

	// A window whose whole table is notional has no figure on any line, the
	// total's included: "-" is not a per-row quirk.
	single := userStatsDoc{
		Project: "aurora", By: userStatsByUser, View: userStatsViewAll,
		You: "someone@example.com", Operator: true, Since: "2026-10-11", Until: "2026-10-18",
		MaxRuns: userStatsMaxRuns,
		Rows:    []userstats.Row{notional},
		Totals: []userstats.Row{{Currency: userstats.CurrencyUSD,
			Counts: userstats.Counts{Runs: 2, Succeeded: 2}, NotionalUSD: 5.00, CountedRuns: 0, AvgCountedUSD: 0}},
	}
	b.Reset()
	if err := writeUserStats(&b, single, "7d", false); err != nil {
		t.Fatal(err)
	}
	userStatsCellsPinned(t, b.String(), head, [][]string{
		{"ci@example.com", "2", "2", "0", "0", "0", "0", "0", "$0.00", "$5.00", "0", "-"},
		{"TOTAL", "2", "2", "0", "0", "0", "0", "0", "$0.00", "$5.00", "0", "-"},
	})
}

// TestRunsStatsRefusesWhatItDoesNotPublish holds --by to the one value the
// command has, rather than accepting a word it silently ignores, and the rest
// of the flags to what they mean.
func TestRunsStatsRefusesWhatItDoesNotPublish(t *testing.T) {
	f := newCloudFixture(t)
	userStatsPlace(t, f, "someone@example.com", nil, []string{"user:someone@example.com"})
	for _, by := range []string{"repo", "model", "person", ""} {
		_, _, err := execute(t, "runs", "stats", "--by", by)
		if err == nil || !strings.Contains(err.Error(), "user") {
			t.Errorf("--by %q: err = %v, want a refusal naming user", by, err)
		}
	}
	for _, args := range [][]string{
		{"runs", "stats", "--repo", "acme/app", "--all"},
		{"runs", "stats", "--max-runs", "0"},
		{"runs", "stats", "--max-runs", "-1"},
		{"runs", "stats", "--since", "lastweek"},
		{"runs", "stats", "extra-argument"},
	} {
		if _, _, err := execute(t, args...); err == nil {
			t.Errorf("%v was accepted", args)
		}
	}
}

// TestRunsStatsRepoAndSinceMatchLs is the shape the two commands share: --repo
// reads one repository, and --since the same window runs ls lists, over UTC
// days.
func TestRunsStatsRepoAndSinceMatchLs(t *testing.T) {
	f := newCloudFixture(t)
	userStatsPlace(t, f, "someone@example.com", nil, []string{"user:someone@example.com"})
	now := time.Now().UTC()
	seedUserRun(t, f, userStatsID(now, 0), "dana@example.com", userRec(runstore.StatusSucceeded, 1.00, runstore.BasisAPIList))
	// Outside the default seven days, and inside --since 0.
	old := time.Now().UTC().AddDate(0, 0, -40)
	seedUserRun(t, f, userStatsID(old, 0), "dana@example.com", userRec(runstore.StatusSucceeded, 9.00, runstore.BasisAPIList))

	inWindow := runsStats(t)
	if len(inWindow.Totals) != 1 || inWindow.Totals[0].Runs != 1 {
		t.Fatalf("the default window = %+v, want only this week's run", inWindow.Totals)
	}
	if inWindow.Since == "" || inWindow.Until == "" {
		t.Errorf("the document names no window: %+v", inWindow)
	}
	whole := runsStats(t, "--since", "0")
	if len(whole.Totals) != 1 || whole.Totals[0].Runs != 2 || whole.Since != "" {
		t.Fatalf("--since 0 = %+v, want the whole bucket and no since date", whole)
	}
	one := runsStats(t, "--repo", "acme/app")
	if len(one.Totals) != 1 || one.Totals[0].Runs != 1 {
		t.Fatalf("--repo acme/app = %+v", one.Totals)
	}
	if whole.MaxRuns != userStatsMaxRuns {
		t.Errorf("max_runs = %d, want the default %d", whole.MaxRuns, userStatsMaxRuns)
	}
}

// TestRunsStatsNamesNoRun is the narrowness the other run commands keep: the
// table and the document are about people and counts, so no run ID, branch,
// commit or URL of any fixture run reaches them.
func TestRunsStatsNamesNoRun(t *testing.T) {
	f := newCloudFixture(t)
	userStatsPlace(t, f, "someone@example.com", nil, []string{"user:someone@example.com"})
	day := time.Now().UTC()
	id := userStatsID(day, 0)
	rec := userRec(runstore.StatusSucceeded, 1.00, runstore.BasisAPIList)
	rec.Branch = "fugaro/runs-stats-fixture-abcd"
	rec.PushedHead = strings.Repeat("ab", 20)
	rec.PR = &runstore.PRRef{Number: 7, URL: "https://github.com/acme/app/pull/7"}
	seedUserRun(t, f, id, "dana@example.com", rec)

	human, _, err := execute(t, "runs", "stats")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(runsStats(t))
	for what, text := range map[string]string{"the table": human, "the document": string(raw)} {
		for _, named := range []string{id, appSlug, rec.Branch, rec.PushedHead, rec.PR.URL} {
			if strings.Contains(text, named) {
				t.Errorf("%s carries %q:\n%s", what, named, text)
			}
		}
	}
}
