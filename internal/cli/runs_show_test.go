package cli

// fugaro runs show: one run's chain and what is left of its limits
// (docs/run-limit-carry-over spec §7, D1055, D1056). The chain is the unit of
// work whose limits its runs share (D1045), so the command answers the question
// a halted run leaves — how much is left — and it answers it from the same two
// places the engine does: the chain's limit out of the run that began it
// (D1041, D1046) and what the chain has spent out of the budget database's
// per-run ledgers and the run records (D1046, D1047).
//
// The figures are pinned by hand here and every read is a fake: a file://
// bucket, the RTDB and Run fakes, and no network at all.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// The three runs every test here shows: the root that began the chain, the
// follow-up of it, and the tip the command is asked about. The root's stored
// task carries the chain's limit, which is the only place a chain's limit
// lives (D1046).
const (
	showRoot = "20261101-090000-a1a1"
	showMid  = "20261101-120000-b2b2"
	showTip  = "20261101-150000-c3c3"
)

// showLimit is the chain limit the root's task carries: one figure in each of
// the four dimensions (D1042).
func showLimit() task.Overrides {
	usd, tokens, rounds := 4.0, int64(800000), 4
	return task.Overrides{MaxUSD: &usd, MaxTokens: &tokens, MaxTime: "45m", MaxReviewRounds: &rounds}
}

// showStarted is when every run of the chain here started, and showElapsed the
// five minutes each of them ran: three runs put 15m0s against the chain's 45m0s
// wall-time limit (D1060 reads the record's elapsed time).
var (
	showStarted = time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	showElapsed = 5 * time.Minute
)

// showHalt is what a halted run of the chain carries in its record: the run cap
// the runner stopped it for, with the sentence it writes beside it.
var showHalt = runstore.Halt{Reason: runstore.HaltRunCap, Scope: "run", At: time.Date(2026, 11, 1, 15, 5, 0, 0, time.UTC), Detail: "run spent $5.00 of $5.00"}

// seedShowRun stores id as a run of the chain the root began: its task carries
// o (only the root's is the chain's limit) and its record is a follow-up of
// previous that ran five minutes, held rounds review rounds and states costUSD
// as its model spend — the figure a chain reads with the budget off (D1046).
func seedShowRun(t *testing.T, f *cloudFixture, id, previous string, o task.Overrides, costUSD float64, rounds int) {
	t.Helper()
	spec := firstRunSpec(id)
	if previous != "" {
		spec = followUpSpec(id, showRoot, previous, 7)
	}
	spec.Overrides = o
	seedSpec(t, f, spec, false)
	rec := prRecord(id, "", 7, costUSD)
	rec.Branch, rec.StartedAt = "fugaro/"+showRoot, showStarted
	finished := showStarted.Add(showElapsed)
	rec.FinishedAt = &finished
	rec.Reviews = make([]runstore.ReviewSummary, rounds)
	if previous != "" {
		rec.FollowUp = &runstore.FollowUp{PR: 7, PreviousRun: previous}
	}
	writeRecord(t, f, id, rec)
}

// showRecord is a run's record as the bucket holds it: the tests that change
// one field of it — the halt, the layers its policy names — read it back,
// change it and write it again.
func showRecord(t *testing.T, f *cloudFixture, id string) *runstore.Record {
	t.Helper()
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	rec, err := runstore.Open(b, appSlug, id).ReadRecord(ctx)
	if err != nil {
		t.Fatalf("result.json of %s: %v", id, err)
	}
	return rec
}

// showRig is the chain with the budget on: the three runs' ledgers hold 2.25
// dollars (reserved less released) and 550000 tokens between them, and the
// records put 15 minutes and two review rounds against the chain's limits.
func showRig(t *testing.T) *cloudFixture {
	t.Helper()
	f := newLaunchBudget(t, "enforce")
	seedChain(t, f.cloudFixture)
	for id, l := range map[string]budget.RunLedger{
		showRoot: {Reserved: 2_000_000, Released: 500_000, Tokens: 400_000},
		showMid:  {Reserved: 1_000_000, Released: 500_000, Tokens: 100_000},
		showTip:  {Reserved: 250_000, Released: 0, Tokens: 50_000},
	} {
		f.db.Set(budget.PathRun(appSlug, id), l)
	}
	return f.cloudFixture
}

// seedChain is the chain's three runs as their tasks and records alone, which
// is every figure a budget-off chain has (D1046).
func seedChain(t *testing.T, f *cloudFixture) {
	t.Helper()
	seedShowRun(t, f, showRoot, "", showLimit(), 1.5, 1)
	seedShowRun(t, f, showMid, showRoot, task.Overrides{}, 0.5, 1)
	seedShowRun(t, f, showTip, showMid, task.Overrides{}, 0.25, 0)
}

// showRow is one dimension's row of runs show's table, as the words after the
// dimension: limit, used, remaining, source. The row is found by its dimension
// cell, which is the vocabulary the JSON prints too.
func showRow(t *testing.T, out, dim string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if fields := strings.Fields(line); len(fields) > 0 && fields[0] == dim {
			return strings.Join(fields[1:], " ")
		}
	}
	t.Fatalf("runs show printed no %s row:\n%s", dim, out)
	return ""
}

// TestRunsShowPrintsTheChainLimits is the headline: for one run the command
// prints the chain it belongs to, each dimension's limit, what the chain has
// used and what is left, and the source of each limit. The figures are three
// runs deep, so a chain read that stopped at the run it was asked about — the
// shortening D1047 exists to prevent — reads differently on every row.
func TestRunsShowPrintsTheChainLimits(t *testing.T) {
	showRig(t)
	out, errOut, err := execute(t, "runs", "show", showTip)
	if err != nil {
		t.Fatalf("runs show %s: %v (%s)", showTip, err, errOut)
	}
	for _, tc := range []struct{ dim, want string }{
		{"usd", "4 dollars 2.25 dollars 1.75 dollars chain"},
		{"tokens", "800000 tokens 550000 tokens 250000 tokens chain"},
		{"time", "45m0s 15m0s 30m0s chain"},
		{"review_rounds", "4 rounds 2 rounds 2 rounds chain"},
	} {
		if got := showRow(t, out, tc.dim); got != tc.want {
			t.Errorf("%s row = %q, want %q\n%s", tc.dim, got, tc.want, out)
		}
	}
	if !strings.Contains(out, showRoot) || !strings.Contains(out, "3 runs") || !strings.Contains(out, "ENFORCED") {
		t.Errorf("runs show does not name the chain it read:\n%s", out)
	}
	if strings.Contains(out, "UNENFORCED") {
		t.Errorf("runs show marks an installation running the budget UNENFORCED:\n%s", out)
	}
}

// TestRunsShowNamesTheChainLayerAsChainSource: the source of a limit is the
// policy layer's own name, read where the merge recorded it, and the chain
// layer's name is policy.SourceChain (F4, D1055). Before the layer had a name
// of its own every layer past the first two was called `branch`, and a limit
// printed as coming from the branch names a place the number did not come from.
//
// A record that carries no policy block is the second half: the limit is still
// the chain launch's own figure — that is what a chain's limit is (D1041) — so
// the layer's name stands where the record names none.
func TestRunsShowNamesTheChainLayerAsChainSource(t *testing.T) {
	t.Run("the run's own record names the layer", func(t *testing.T) {
		f := showRig(t)
		rec := showRecord(t, f, showTip)
		sources := map[string]string{}
		for _, key := range []string{policy.KeyPerRunUSD, policy.KeyMaxRunTokens, policy.KeyMaxRunTime, policy.KeyMaxReviewRounds} {
			sources[key] = policy.SourceChain
		}
		rec.Policy = &runstore.PolicyRecord{Sources: sources}
		writeRecord(t, f, showTip, rec)

		out, errOut, err := execute(t, "runs", "show", showTip)
		if err != nil {
			t.Fatalf("runs show: %v (%s)", err, errOut)
		}
		for _, dim := range []string{"usd", "tokens", "time", "review_rounds"} {
			if got := showRow(t, out, dim); !strings.HasSuffix(got, " "+policy.SourceChain) {
				t.Errorf("%s row = %q, want its source to be %q\n%s", dim, got, policy.SourceChain, out)
			}
		}
		if strings.Contains(out, policy.SourceBranch) {
			t.Errorf("a chain limit is printed as coming from the branch (F4):\n%s", out)
		}
	})
	t.Run("a record with no policy block", func(t *testing.T) {
		showRig(t)
		out, errOut, err := execute(t, "runs", "show", showTip)
		if err != nil {
			t.Fatalf("runs show: %v (%s)", err, errOut)
		}
		if got := showRow(t, out, "usd"); !strings.HasSuffix(got, " "+policy.SourceChain) {
			t.Errorf("usd row = %q, want the chain layer's own name as its source\n%s", got, out)
		}
	})
}

// TestRunsShowMarksABudgetOffChainUnenforced: with the budget off there is no
// ledger, so the chain's dollar figure is the records' and the token dimension
// has no figure at all — the record carries no whole-run token total, and the
// cache sums it does carry are not one (D1046, D1051, T10). The command marks
// the chain UNENFORCED and prints the token dimension as unmeasured rather than
// approximating it or reading the limit as unspent.
func TestRunsShowMarksABudgetOffChainUnenforced(t *testing.T) {
	f := newCloudFixture(t)
	seedChain(t, f)

	out, errOut, err := execute(t, "runs", "show", showTip)
	if err != nil {
		t.Fatalf("runs show: %v (%s)", err, errOut)
	}
	if !strings.Contains(out, "UNENFORCED") {
		t.Errorf("runs show does not mark a budget-off chain UNENFORCED:\n%s", out)
	}
	if got, want := showRow(t, out, "usd"), "4 dollars 2.25 dollars 1.75 dollars chain"; got != want {
		t.Errorf("usd row = %q, want the records' own figure %q\n%s", got, want, out)
	}
	if got, want := showRow(t, out, "tokens"), "800000 tokens unmeasured unmeasured chain"; got != want {
		t.Errorf("tokens row = %q, want %q\n%s", got, want, out)
	}
}

// TestRunsShowNamesTheHalt: a halted run's first question is how much is left
// (D1055), and the halt itself is the rest of the answer — why it stopped, over
// what, and when.
func TestRunsShowNamesTheHalt(t *testing.T) {
	f := showRig(t)
	rec := showRecord(t, f, showTip)
	rec.Status, rec.Halt = runstore.StatusHalted, &showHalt
	writeRecord(t, f, showTip, rec)

	out, errOut, err := execute(t, "runs", "show", showTip)
	if err != nil {
		t.Fatalf("runs show: %v (%s)", err, errOut)
	}
	want := "Halted:   run_cap (run) at 2026-11-01T15:05:00Z: run spent $5.00 of $5.00"
	if !strings.Contains(out, want) {
		t.Errorf("runs show lacks %q:\n%s", want, out)
	}
}

// TestRunsShowJSONMatchesTheSchema validates the document against
// schemas/runs-show.schema.json, the contract the console, the coordinator and
// an orchestrator read it by (D1056), and pins the members a consumer branches
// on. The shape is the one L7's launch reply carries for `chain` and `limits[]`,
// so the two cannot drift.
func TestRunsShowJSONMatchesTheSchema(t *testing.T) {
	showRig(t)
	out, errOut, err := execute(t, "runs", "show", "--json", showTip)
	if err != nil {
		t.Fatalf("runs show --json: %v (%s)", err, errOut)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "runs-show.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := doc.(map[string]any)["$id"].(string)
	if id == "" {
		t.Fatal("schemas/runs-show.schema.json has no $id")
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(out))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("the document does not match schemas/runs-show.schema.json: %v\n%s", err, out)
	}

	var got struct {
		Version  int    `json:"version"`
		Repo     string `json:"repo"`
		Run      string `json:"run"`
		Chain    string `json:"chain"`
		RunCount int    `json:"run_count"`
		Enforced bool   `json:"enforced"`
		Limits   []struct {
			Dimension string   `json:"dimension"`
			Limit     float64  `json:"limit"`
			Used      *float64 `json:"used"`
			Remaining *float64 `json:"remaining"`
			Source    string   `json:"source"`
		} `json:"limits"`
		Halt *runstore.Halt `json:"halt"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 {
		t.Errorf("version = %d, and the document's first version is 1", got.Version)
	}
	if got.Repo != "acme/app" || got.Run != showTip {
		t.Errorf("the document is about %s/%s, want acme/app/%s", got.Repo, got.Run, showTip)
	}
	if got.Chain != showRoot || got.RunCount != 3 || !got.Enforced {
		t.Errorf("chain = %q, %d runs, enforced = %v, want %q and its three runs, enforced", got.Chain, got.RunCount, got.Enforced, showRoot)
	}
	want := []struct {
		dim                  string
		limit, used, remains float64
	}{
		{"usd", 4, 2.25, 1.75},
		{"tokens", 800000, 550000, 250000},
		{"time", 2700, 900, 1800},
		{"review_rounds", 4, 2, 2},
	}
	if len(got.Limits) != len(want) {
		t.Fatalf("limits holds %d dimensions, want %d: %+v", len(got.Limits), len(want), got.Limits)
	}
	for i, w := range want {
		l := got.Limits[i]
		if l.Dimension != w.dim || l.Limit != w.limit || l.Source != policy.SourceChain {
			t.Errorf("limits[%d] = %+v, want %s with limit %v from %q", i, l, w.dim, w.limit, policy.SourceChain)
			continue
		}
		if l.Used == nil || l.Remaining == nil || *l.Used != w.used || *l.Remaining != w.remains {
			t.Errorf("limits[%d] = %+v, want %v used and %v left", i, l, w.used, w.remains)
		}
	}
	if got.Halt != nil {
		t.Errorf("halt = %+v, want none for a run that did not halt", got.Halt)
	}
}

// TestRunsShowRefusesARunThatIsNotInThisRepository: --repo names the repository
// a bare run ID belongs to, so a run that already carries a slug has to agree
// with it — a chain is one repository's runs, and another repository's figures
// are not this one's (D1045). The refusal names both repositories.
func TestRunsShowRefusesARunThatIsNotInThisRepository(t *testing.T) {
	f := chainRig(t)
	addOtherRepo(t, f)
	other := mustSlug("github", "acme/other")
	seedTaskIn(t, f, other, showRoot)

	_, _, err := execute(t, "runs", "show", other+"/"+showRoot, "--repo", limitsRepo)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "is in "+other) || !strings.Contains(err.Error(), "--repo names "+appSlug) {
		t.Fatalf("runs show %s/%s --repo %s: err = %v, want a refusal naming both repositories", other, showRoot, limitsRepo, err)
	}
	// The run itself is readable where it is: the refusal is about --repo.
	if _, _, err := execute(t, "runs", "show", other+"/"+showRoot, "--repo", "acme/other"); err != nil {
		t.Fatalf("runs show %s/%s --repo acme/other = %v", other, showRoot, err)
	}
}
