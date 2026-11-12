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
	"github.com/dimipaun/fugaro/internal/testutil"
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

// TestRunsShowNamesTheChainLayerAsChainSource: a chain limit's figure is the
// root launch's own, written by its `--max-*` flags and merged as the chain
// layer (D1041, D1043), so its source is policy.SourceChain (F4, D1055) — and
// it is that whatever the shown run's own record says. Before the layer had a
// name of its own every layer past the first two was called `branch`, and a
// limit printed as coming from the branch names a place the number did not come
// from (spec §2).
//
// The record is the trap. Sources names the layer that set each *merge* key,
// and the dollar key is also the repository's own per-run cap: a repository
// whose fugaro.yaml sets budget.per_run_usd writes `branch` there, so a source
// read off the shown run's record labels the chain's figure with a layer that
// set a different number. The tip's record here names all four other layers.
//
// A record that carries no policy block is the other half: the silence is the
// chain layer's too.
func TestRunsShowNamesTheChainLayerAsChainSource(t *testing.T) {
	t.Run("the record's merge names other layers", func(t *testing.T) {
		f := showRig(t)
		rec := showRecord(t, f, showTip)
		rec.Policy = &runstore.PolicyRecord{Sources: map[string]string{
			policy.KeyPerRunUSD:       policy.SourceBranch,
			policy.KeyMaxRunTokens:    policy.SourceCeiling,
			policy.KeyMaxRunTime:      policy.SourcePolicy,
			policy.KeyMaxReviewRounds: policy.SourceDefaultBranch,
		}}
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
		if strings.Contains(out, policy.SourceBranch) || strings.Contains(out, policy.SourceCeiling) {
			t.Errorf("a chain limit is printed as coming from the layer that set the run's own cap (F4):\n%s", out)
		}
		// The same column travels in the document the console, the coordinator
		// and an orchestrator read (D1056).
		jout, jerrOut, err := execute(t, "runs", "show", "--json", showTip)
		if err != nil {
			t.Fatalf("runs show --json: %v (%s)", err, jerrOut)
		}
		var doc struct {
			Limits []struct {
				Source string `json:"source"`
			} `json:"limits"`
		}
		if err := json.Unmarshal([]byte(jout), &doc); err != nil {
			t.Fatal(err)
		}
		for i, l := range doc.Limits {
			if l.Source != policy.SourceChain {
				t.Errorf("limits[%d].source = %q, want %q", i, l.Source, policy.SourceChain)
			}
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

// TestRunsShowSanitizesTheRecordsText: part of what this view prints comes out
// of result.json, an object the job's service account — and so the agent — can
// write, and a control character in a printed string rewrites the operator's
// screen, plants a hyperlink or sets the clipboard (safetext.go). The record's
// text in this view is the repository's name and the halt's own sentence; what
// a row's source cell holds is the chain layer's own name and not the record's
// (ChainLimit.Source), so the cell is defended rather than exercised here.
func TestRunsShowSanitizesTheRecordsText(t *testing.T) {
	f := showRig(t)
	rec := showRecord(t, f, showTip)
	h := showHalt
	h.Detail = "run spent \x1b[2J$5.00 of $5.00\nand $9.00 more\x07"
	rec.Repo, rec.Status, rec.Halt = "acme/app\x1b]52;c;AAAA\x07", runstore.StatusHalted, &h
	writeRecord(t, f, showTip, rec)

	out, errOut, err := execute(t, "runs", "show", showTip)
	if err != nil {
		t.Fatalf("runs show: %v (%s)", err, errOut)
	}
	text := out + errOut
	for _, bad := range []string{"\x1b", "\x07", "\r"} {
		if strings.Contains(text, bad) {
			t.Errorf("runs show prints %q from the record:\n%s", bad, text)
		}
	}
	// A newline of the record's own would forge a line of the view: the halt
	// stays one line, and what the record wrote after its newline is inside
	// that line rather than at the head of one of its own.
	if strings.Contains(text, "\nand $9.00 more") {
		t.Errorf("a newline in the record's halt opened a line of the view:\n%s", text)
	}
	if !strings.Contains(text, "and $9.00 more") {
		t.Errorf("runs show dropped the halt's own text rather than neutralizing its controls:\n%s", text)
	}
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

// dropStoredTask removes a run's task.json from the fixture's bucket, which is
// what an aged-out chain root looks like from here: the bucket drops a run's
// objects together once the run is older than its window, and the chain's limit
// lives in that one object (D1046).
func dropStoredTask(t *testing.T, f *cloudFixture, id string) {
	t.Helper()
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := runstore.Open(b, appSlug, id).DeleteFile(ctx, "task.json"); err != nil {
		t.Fatalf("deleting task.json of %s: %v", id, err)
	}
}

// TestRunsShowSaysWhenTheChainsLimitCouldNotBeRead: the chain's limit lives in
// the stored task of the run that began it (D1046) and the bucket ages a run's
// objects out, so a chain whose root is older than that window has no figure
// left to read. The empty limits that follows is not a chain that bounds
// nothing — read for each other, an unknown limit reads as an unlimited one and
// the answer says the opposite of the truth, which is what the null of an
// unmeasured dimension prevents one level down (D1051). limits_read is the
// member that keeps the two apart in the document too (D1056), and the chain
// itself is still answered: the runs the link reached and what they spent are a
// fact about them whatever the root's object held.
func TestRunsShowSaysWhenTheChainsLimitCouldNotBeRead(t *testing.T) {
	t.Run("the root's task is gone", func(t *testing.T) {
		f := showRig(t)
		dropStoredTask(t, f, showRoot)

		out, errOut, err := execute(t, "runs", "show", showTip)
		if err != nil {
			t.Fatalf("runs show %s: %v (%s)", showTip, err, errOut)
		}
		if !strings.Contains(out, "the chain's limit could not be read") {
			t.Errorf("runs show does not say the chain's limit could not be read:\n%s", out)
		}
		if strings.Contains(out, "bounds none") {
			t.Errorf("a chain whose limit could not be read is printed as one that bounds nothing:\n%s", out)
		}
		if !strings.Contains(out, showRoot) || !strings.Contains(out, "3 runs") {
			t.Errorf("runs show drops what it did read of the chain:\n%s", out)
		}

		jout, jerrOut, err := execute(t, "runs", "show", "--json", showTip)
		if err != nil {
			t.Fatalf("runs show --json: %v (%s)", err, jerrOut)
		}
		var got struct {
			Chain      string `json:"chain"`
			LimitsRead bool   `json:"limits_read"`
			Limits     []any  `json:"limits"`
		}
		if err := json.Unmarshal([]byte(jout), &got); err != nil {
			t.Fatal(err)
		}
		if got.Chain != showRoot || got.LimitsRead || len(got.Limits) != 0 {
			t.Errorf("chain = %q, limits_read = %v, %d limits: want the chain read, its limit not, and no rows", got.Chain, got.LimitsRead, len(got.Limits))
		}
	})
	t.Run("a chain that bounds nothing", func(t *testing.T) {
		f := newLaunchBudget(t, "enforce")
		seedShowRun(t, f.cloudFixture, showRoot, "", task.Overrides{}, 1.0, 0)
		seedShowRun(t, f.cloudFixture, showMid, showRoot, task.Overrides{}, 0.5, 0)
		seedShowRun(t, f.cloudFixture, showTip, showMid, task.Overrides{}, 0.25, 0)

		out, errOut, err := execute(t, "runs", "show", showTip)
		if err != nil {
			t.Fatalf("runs show %s: %v (%s)", showTip, err, errOut)
		}
		if !strings.Contains(out, "bounds none of the four dimensions") {
			t.Errorf("runs show does not say a chain with no limit bounds nothing:\n%s", out)
		}
		if strings.Contains(out, "could not be read") {
			t.Errorf("a chain that bounds nothing is printed as one whose limit could not be read:\n%s", out)
		}
		// The same two documents, told apart by the same member: the limit was
		// read here and it is empty, and a consumer that read the two empty
		// limits arrays for each other would bound neither chain.
		jout, jerrOut, err := execute(t, "runs", "show", "--json", showTip)
		if err != nil {
			t.Fatalf("runs show --json: %v (%s)", err, jerrOut)
		}
		var got struct {
			LimitsRead bool  `json:"limits_read"`
			Limits     []any `json:"limits"`
		}
		if err := json.Unmarshal([]byte(jout), &got); err != nil {
			t.Fatal(err)
		}
		if !got.LimitsRead || len(got.Limits) != 0 {
			t.Errorf("limits_read = %v with %d limits, want a limit that was read and bounds nothing", got.LimitsRead, len(got.Limits))
		}
	})
}

// TestRunsShowResolvesARepoPath: --repo is documented as taking a path inside a
// checkout, and repoArg is the one resolver of that value (repo_arg.go), which
// every sibling command runs. Without it the path is handed to the run resolver
// as though it were part of a run reference, and refused with a sentence naming
// neither the flag nor the value.
func TestRunsShowResolvesARepoPath(t *testing.T) {
	testutil.IsolateGit(t)
	showRig(t)
	checkout := repoArgCheckout(t, filepath.Join(t.TempDir(), "checkout"), githubOrigin)

	out, errOut, err := execute(t, "runs", "show", showTip, "--repo", checkout)
	if err != nil {
		t.Fatalf("runs show %s --repo %s: %v (%s)", showTip, checkout, err, errOut)
	}
	if !strings.Contains(out, showRoot) || !strings.Contains(out, "ENFORCED") {
		t.Errorf("runs show --repo <path> did not resolve the checkout to its repository:\n%s", out)
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
		Version    int    `json:"version"`
		Repo       string `json:"repo"`
		Run        string `json:"run"`
		Chain      string `json:"chain"`
		RunCount   int    `json:"run_count"`
		Enforced   bool   `json:"enforced"`
		LimitsRead bool   `json:"limits_read"`
		Limits     []struct {
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
	// Repo is the repository's own name and Run is <storage-slug>/<run-id> —
	// the pair runview.Row and launchResult carry — while the chain is a bare
	// run id, the chain being one repository's runs (D1045).
	if got.Repo != "acme/app" || got.Run != appSlug+"/"+showTip {
		t.Errorf("the document is about %s/%s, want acme/app and %s/%s", got.Repo, got.Run, appSlug, showTip)
	}
	if got.Chain != showRoot || got.RunCount != 3 || !got.Enforced {
		t.Errorf("chain = %q, %d runs, enforced = %v, want %q and its three runs, enforced", got.Chain, got.RunCount, got.Enforced, showRoot)
	}
	// The limit was read, and the rows below are it: limits_read false would say
	// the chain's figure was never read, which is the other reading of a
	// document whose limits are empty (ChainLimits.LimitsRead).
	if !got.LimitsRead {
		t.Errorf("limits_read = false for a chain whose root's stored task holds the limit")
	}
	// The amounts are the engine's own figures, unconverted: micro-dollars for
	// the money — the unit the ledger, the caps and every other document count
	// in (runs summary's spent_micros) — whole tokens, seconds, a count. The
	// launch reply carries this same block (D1056), so the two documents print
	// one number for one figure and a conversion in one of them would be drift.
	want := []struct {
		dim                  string
		limit, used, remains float64
	}{
		{"usd", 4_000_000, 2_250_000, 1_750_000},
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
