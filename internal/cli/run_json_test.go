package cli

// `fugaro run --json`'s reply carries the chain the launch joined and what is
// left of its limits (docs/run-limit-carry-over L7, spec §7, D1056): the same
// members `fugaro runs show --json` prints, written by the same builder, so a
// consumer reading the document a launch answered and the document runs show
// prints is reading one shape and not two.
//
// The additions are additive, and these tests pin that from both sides: the
// reply of a launch that joins a chain names the run that began it — never the
// run the caller continued, which would be a chain of one — and the reply of a
// launch that begins one is byte-identical to what it was before (no chain
// members at all), which is what a consumer that ignores keys it does not know
// reads (the console's own ruling of 2026-10-30: the CLI's JSON only grows).
//
// Every figure here is pinned by hand and every read is a fake: a file://
// bucket, the RTDB and Run fakes, and no network at all.

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// The chain every test here launches into: a run that began it, a follow-up of
// that run, and the tip the launch continues. The limit lives in the stored
// task of the root alone (D1041, D1046), and the root's id is what the reply's
// `chain` must name — the tip's id would be the shortening D1047 forbids.
var (
	jsonRoot = wallRunIDAt(3, "090000", "11a1")
	jsonMid  = wallRunIDAt(2, "090000", "22b2")
	jsonTip  = wallRunIDAt(1, "090000", "33c3")
	jsonFu   = wallRunIDAt(0, "130000", "44d4")
)

// jsonLimit is the chain's own limit, one figure in each of the four
// dimensions (D1042), under the caps the limits checkout's file sets — the
// figures a launch continuing the chain merely inherits, so no tightening
// refusal is in play here (run_limits_test.go).
func jsonLimit() task.Overrides {
	usd, tokens, rounds := 4.0, int64(800000), 4
	return task.Overrides{MaxUSD: &usd, MaxTokens: &tokens, MaxTime: "45m", MaxReviewRounds: &rounds}
}

// jsonRecorded is when each run of the chain here started and how long it ran:
// the record's own elapsed time, which is the figure D1060 reads.
var (
	jsonStarted = time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	jsonElapsed = 5 * time.Minute
)

// jsonChainRun stores id as a run of the chain: a task carrying o (the root's
// alone is the chain's limit) and a record linked to previous that states
// costUSD as its model spend and holds rounds review rounds. With the budget
// off, the record's cost is the chain's dollar figure (D1046).
func jsonChainRun(t *testing.T, f *cloudFixture, id, previous string, o task.Overrides, costUSD float64, rounds int) {
	t.Helper()
	spec := firstRunSpec(id)
	spec.Overrides = o
	seedSpec(t, f, spec, false)
	rec := prRecord(id, "", 0, costUSD)
	rec.Branch, rec.Status, rec.Stage, rec.Outcome = "fugaro/"+jsonRoot, runstore.StatusHalted, "writeback", runstore.OutcomeNone
	rec.PR, rec.PushedHead = nil, ""
	rec.StartedAt = jsonStarted
	finished := jsonStarted.Add(jsonElapsed)
	rec.FinishedAt = &finished
	rec.Reviews = make([]runstore.ReviewSummary, rounds)
	if previous != "" {
		rec.FollowUp = &runstore.FollowUp{PreviousRun: previous}
	}
	writeRecord(t, f, id, rec)
}

// jsonChain is the chain's three runs as their tasks and records alone, which
// is every figure a budget-off chain has (D1046): 2.25 dollars of the limit's
// 4 spent, 15m of its 45m and 2 of its 4 review rounds, with the token
// dimension unmeasured (no run record carries a token total, D1051).
func jsonChain(t *testing.T, f *cloudFixture) {
	t.Helper()
	jsonChainRun(t, f, jsonRoot, "", jsonLimit(), 1.5, 1)
	jsonChainRun(t, f, jsonMid, jsonRoot, task.Overrides{}, 0.5, 1)
	jsonChainRun(t, f, jsonTip, jsonMid, task.Overrides{}, 0.25, 0)
}

// jsonLaunchReply is the reply as its consumer reads it: the members this work
// adds, decoded off the document and never off the Go type, because the
// document is the contract (D1056).
type jsonLaunchReply struct {
	RunID      string       `json:"run_id"`
	Status     string       `json:"status"`
	Chain      string       `json:"chain"`
	RunCount   int          `json:"run_count"`
	Enforced   bool         `json:"enforced"`
	LimitsRead bool         `json:"limits_read"`
	Limits     []ChainLimit `json:"limits"`
}

// jsonReply launches a run that continues the chain's tip and reads the reply
// back, both as its members and as the raw document.
func jsonReply(t *testing.T, f *cloudFixture) (jsonLaunchReply, map[string]any) {
	t.Helper()
	out, errOut, err := execute(t, "run", "--repo", limitsRepo, "--continues", jsonTip, "--run-id", jsonFu, "--json", "A task")
	if err != nil {
		t.Fatalf("run --continues: %v (%s)", err, errOut)
	}
	var res jsonLaunchReply
	var raw map[string]any
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("the reply is not one JSON object: %v\n%s", err, out)
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("the reply does not decode: %v\n%s", err, out)
	}
	return res, raw
}

// jsonLimitRows is the chain's limits as both documents must print them: the
// four figures of the limit, what the three runs spent of each, and the chain
// layer as every row's source (F4, D1055). The token row is the one dimension
// the budget decides: with the budget off no run record carries a token total,
// so it is reported unmeasured rather than approximated (D1051, F8).
func jsonLimitRows(measuredTokens bool) []ChainLimit {
	usdLimit, usdUsed, usdLeft := runstore.Amount(4_000_000), runstore.Amount(2_250_000), runstore.Amount(1_750_000)
	timeLimit, timeUsed, timeLeft := runstore.Amount(2700), runstore.Amount(900), runstore.Amount(1800)
	roundsLimit, roundsUsed, roundsLeft := runstore.Amount(4), runstore.Amount(2), runstore.Amount(2)
	tokensLimit, tokensUsed, tokensLeft := runstore.Amount(800000), runstore.Amount(550000), runstore.Amount(250000)
	rows := []ChainLimit{
		{Dimension: runstore.USD, Limit: usdLimit, Used: &usdUsed, Remaining: &usdLeft, Source: policy.SourceChain},
		{Dimension: runstore.Tokens, Limit: tokensLimit, Source: policy.SourceChain},
		{Dimension: runstore.Time, Limit: timeLimit, Used: &timeUsed, Remaining: &timeLeft, Source: policy.SourceChain},
		{Dimension: runstore.ReviewRounds, Limit: roundsLimit, Used: &roundsUsed, Remaining: &roundsLeft, Source: policy.SourceChain},
	}
	if measuredTokens {
		rows[1].Used, rows[1].Remaining = &tokensUsed, &tokensLeft
	}
	return rows
}

// TestLaunchJSONCarriesTheChain: the reply of a launch that joins a chain names
// the run that began it, how many runs the link reached, whether the figures
// are the budget database's (D1046), and one row per dimension the chain's own
// limit bounds — the figures the caller needs to know what the run it just
// started may still spend, read from the chain behind the link it named and not
// from the run it named (D1047).
func TestLaunchJSONCarriesTheChain(t *testing.T) {
	f := chainRig(t)
	jsonChain(t, f)
	res, raw := jsonReply(t, f)

	if res.Status != "launched" || res.RunID != jsonFu {
		t.Fatalf("reply = %+v, want %s launched", res, jsonFu)
	}
	if res.Chain != jsonRoot {
		t.Errorf("chain = %q, want the run that began it (%s), not the run the launch continued (%s)\n%s", res.Chain, jsonRoot, jsonTip, raw)
	}
	if res.RunCount != 3 || !res.LimitsRead {
		t.Errorf("reply = %+v, want the three runs of the chain and its limit read", res)
	}
	if want := jsonLimitRows(false); !reflect.DeepEqual(res.Limits, want) {
		t.Errorf("limits = %+v, want %+v", res.Limits, want)
	}
}

// TestLaunchJSONLimitsMatchRunsShow: the reply and `runs show --json` are one
// document written twice — the launch reply's members are the block the command
// prints (D1056), so a consumer that reads one and then the other is not left
// with two spellings of a figure or with a member one of them lacks.
//
// Both documents are read from the same anchor: the run the launch continued,
// which is the chain's own read — the run that just started has no record yet,
// and a chain read from it is a chain of one, which is the shortening D1047
// forbids. The launch's own figures are a snapshot taken as the run started;
// runs show of the chain is the same read at the same moment.
func TestLaunchJSONLimitsMatchRunsShow(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	limitsImage(t, f.cloudFixture)
	limitsCheckout(t)
	jsonChain(t, f.cloudFixture)
	// The ledger is the chain's dollar and token figure while the budget is on
	// (D1046): the three runs hold 2.25 dollars and 550000 tokens between them,
	// whatever their records state.
	for id, l := range map[string]budget.RunLedger{
		jsonRoot: {Reserved: 2_000_000, Released: 500_000, Tokens: 400_000},
		jsonMid:  {Reserved: 1_000_000, Released: 500_000, Tokens: 100_000},
		jsonTip:  {Reserved: 250_000, Released: 0, Tokens: 50_000},
	} {
		f.db.Set(budget.PathRun(appSlug, id), l)
	}
	res, _ := jsonReply(t, f.cloudFixture)

	out, errOut, err := execute(t, "runs", "show", "--json", jsonTip)
	if err != nil {
		t.Fatalf("runs show --json: %v (%s)", err, errOut)
	}
	var show jsonLaunchReply
	if err := json.Unmarshal([]byte(out), &show); err != nil {
		t.Fatalf("runs show --json does not decode: %v\n%s", err, out)
	}
	if res.Chain != show.Chain || res.RunCount != show.RunCount || res.Enforced != show.Enforced || res.LimitsRead != show.LimitsRead {
		t.Errorf("the reply says %+v where runs show says %+v", res, show)
	}
	if want := jsonLimitRows(true); !reflect.DeepEqual(res.Limits, want) || !reflect.DeepEqual(show.Limits, want) {
		t.Errorf("limits = %+v and %+v, want %+v", res.Limits, show.Limits, want)
	}
}

// TestLaunchJSONOmitsTheChainForAFirstRun: a launch that begins a chain joins
// none, so its reply carries no chain member at all — no `chain`, no `limits[]`
// and no `enforced` — and is what every consumer read before this work existed.
// A reply that printed the run it just started as its own chain, or an empty
// `enforced` read as false, would tell a caller the opposite of the truth about
// a chain the launch itself began (D1041).
func TestLaunchJSONOmitsTheChainForAFirstRun(t *testing.T) {
	newCloudFixture(t)
	out, errOut, err := execute(t, "run", "--repo", "acme/app", "--run-id", jsonFu, "--json", "Add a feature")
	if err != nil {
		t.Fatalf("run: %v (%s)", err, errOut)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("the reply is not one JSON object: %v\n%s", err, out)
	}
	for _, key := range []string{"chain", "run_count", "enforced", "limits_read", "limits"} {
		if v, ok := raw[key]; ok {
			t.Errorf("the reply of a launch that begins a chain carries %s = %v:\n%s", key, v, out)
		}
	}
	if raw["status"] != "launched" || raw["run_id"] != jsonFu {
		t.Errorf("reply = %v, want %s launched", raw, jsonFu)
	}
}

// TestLaunchJSONMarksAnUnenforcedChain (D1046): with the budget off there is no
// ledger, so the chain's dollar figure is the records' own — which a launcher
// can write (F21) — and the token dimension has no figure at all. The reply
// marks the chain UNENFORCED, so a consumer does not read a report as a stop,
// and the unmeasured dimension stays absent rather than becoming a zero the
// chain never spent (D1051, F8, T10).
func TestLaunchJSONMarksAnUnenforcedChain(t *testing.T) {
	f := chainRig(t)
	jsonChain(t, f)
	res, _ := jsonReply(t, f)

	if res.Enforced {
		t.Errorf("reply = %+v, want a budget-off chain marked UNENFORCED", res)
	}
	if res.Chain != jsonRoot {
		t.Errorf("chain = %q, want %q", res.Chain, jsonRoot)
	}
	if want := jsonLimitRows(false); !reflect.DeepEqual(res.Limits, want) {
		t.Errorf("limits = %+v, want %+v", res.Limits, want)
	}
	for _, row := range res.Limits {
		if row.Dimension == runstore.Tokens && (row.Used != nil || row.Remaining != nil) {
			t.Errorf("the token row = %+v, want it unmeasured rather than a figure the records do not hold", row)
		}
	}
}

// TestLaunchJSONStaysReadableToAConsumerThatIgnoresTheAdditions: the members
// this work adds are additions and nothing else (D1056, spec §7) — no member is
// renamed, retyped or removed — so a consumer that names only what it knew
// before reads the same facts out of the reply, which is what lets an older
// console read a newer CLI's document.
func TestLaunchJSONStaysReadableToAConsumerThatIgnoresTheAdditions(t *testing.T) {
	f := chainRig(t)
	jsonChain(t, f)
	out, errOut, err := execute(t, "run", "--repo", limitsRepo, "--continues", jsonTip, "--run-id", jsonFu, "--json", "A task")
	if err != nil {
		t.Fatalf("run --continues: %v (%s)", err, errOut)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("the reply is not one JSON object: %v\n%s", err, out)
	}
	if _, ok := raw["limits"]; !ok {
		t.Fatalf("the reply under test carries none of the additions:\n%s", out)
	}
	// The reader the reply had before this work: the launch's own identity,
	// which is every member a consumer drew before the chain members existed.
	var older struct {
		Run       string `json:"run"`
		Repo      string `json:"repo"`
		RunID     string `json:"run_id"`
		Execution string `json:"execution"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal([]byte(out), &older); err != nil {
		t.Fatalf("the reply does not decode into the reader that predates the additions: %v\n%s", err, out)
	}
	if older.Status != "launched" || older.RunID != jsonFu || older.Run != appSlug+"/"+jsonFu ||
		older.Repo != limitsRepo || older.Execution == "" {
		t.Errorf("the reader that ignores the additions read %+v:\n%s", older, out)
	}
}
