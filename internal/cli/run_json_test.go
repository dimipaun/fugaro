package cli

// `fugaro run --json`'s reply carries the chain the launch joined and what is
// left of its limits (docs/run-limit-carry-over L7, spec §7, D1056): the same
// members `fugaro runs show --json` prints, written by the same builder, so a
// consumer reading the document a launch answered and the document runs show
// prints is reading one shape and not two.
//
// The chain here is a pull request's, which is the one shape whose runs link
// each other through their records (D1045, §3): the launch follows the pull
// request up, so the run it starts writes a record that names the run it
// continues, and runs show of that run reaches the same chain the reply named —
// with the launch itself counted among its runs. A chain continued by
// --continues has no such record to write, and the test that pins the reply
// there says so.
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

// jsonPR is the pull request the chain every test here belongs to.
const jsonPR = 7

// The chain's three runs — the one that began it and holds its limit (D1041,
// D1046), the follow-up of it, and the tip the launch follows up — and jsonFu,
// the launch itself. The ids are minted from the wall clock because that is the
// clock run --pr reads (wallRunIDAt): a follow-up's window is followUpLookback
// back from time.Now(), which no test can move.
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

// jsonElapsed is the five minutes each run of the chain ran: the record's own
// elapsed time, which is the figure D1060 reads, and three of them put 15m0s
// against the chain's 45m0s.
const jsonElapsed = 5 * time.Minute

// jsonDaysAgo is when the chain's runs started, as days back from now: inside
// the window resolvePR finds a follow-up's previous run through
// (followUpLookback), and ordered by the clock a --pr launch's own id is minted
// from.
func jsonStarted(daysAgo int) time.Time { return time.Now().AddDate(0, 0, -daysAgo) }

// jsonChainRun stores id as a run of root's chain: a task carrying o (the
// root's alone is the chain's limit), a record that pushed to the pull request
// — which is what resolvePR finds a follow-up's previous run through — and the
// link back to previous, which is what makes the run a member of the chain.
// With the budget off, the record's cost is the chain's dollar figure (D1046).
func jsonChainRun(t *testing.T, f *cloudFixture, id, root, previous string, o task.Overrides, costUSD float64, rounds, daysAgo int) {
	t.Helper()
	spec := firstRunSpec(id)
	if previous != "" {
		spec = followUpSpec(id, root, previous, jsonPR)
	}
	spec.Overrides = o
	seedSpec(t, f, spec, false)
	rec := prRecord(id, "", jsonPR, costUSD)
	rec.Branch, rec.StartedAt = "fugaro/"+root, jsonStarted(daysAgo)
	finished := rec.StartedAt.Add(jsonElapsed)
	rec.FinishedAt = &finished
	rec.Reviews = make([]runstore.ReviewSummary, rounds)
	if previous != "" {
		rec.FollowUp = &runstore.FollowUp{PR: jsonPR, PreviousRun: previous}
	}
	writeRecord(t, f, id, rec)
}

// jsonChain is the chain's three runs as their tasks and records alone, which
// is every figure a budget-off chain has (D1046): 2.25 dollars of the limit's
// 4 spent, 15m of its 45m and 2 of its 4 review rounds, with the token
// dimension unmeasured (no run record carries a token total, D1051).
func jsonChain(t *testing.T, f *cloudFixture) {
	t.Helper()
	jsonChainRun(t, f, jsonRoot, jsonRoot, "", jsonLimit(), 1.5, 1, 3)
	jsonChainRun(t, f, jsonMid, jsonRoot, jsonRoot, task.Overrides{}, 0.5, 1, 2)
	jsonChainRun(t, f, jsonTip, jsonRoot, jsonMid, task.Overrides{}, 0.25, 0, 1)
}

// jsonAgedChain is a chain whose first run the bucket no longer holds: the two
// follow-ups are there and link to a run 95 days old, which is past the window
// the bucket ages a run's objects out over, and that run has nothing stored at
// all — the state that makes a chain read stop at a gap (TestRunPRRootExpired,
// followup_test.go).
func jsonAgedChain(t *testing.T, f *cloudFixture) {
	t.Helper()
	gone := wallRunIDAt(95, "090000", "a0a0")
	jsonChainRun(t, f, jsonMid, gone, gone, task.Overrides{}, 0.5, 1, 2)
	jsonChainRun(t, f, jsonTip, gone, jsonMid, task.Overrides{}, 0.25, 0, 1)
}

// jsonStartedRun writes the record of the run this launch started, as the
// runner writes one: the link back to the run it continues, which is what puts
// the run in the same chain, with nothing spent and no time behind it. The
// reply is read as the run starts, so what the chain holds for this run is
// nothing, and the chain runs show reads from it is the chain the reply names.
func jsonStartedRun(t *testing.T, f *cloudFixture) {
	t.Helper()
	rec := prRecord(jsonFu, "", jsonPR, 0)
	rec.Branch, rec.StartedAt = "fugaro/"+jsonRoot, jsonStarted(0)
	finished := rec.StartedAt
	rec.FinishedAt = &finished
	rec.FollowUp = &runstore.FollowUp{PR: jsonPR, PreviousRun: jsonTip}
	writeRecord(t, f, jsonFu, rec)
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

// jsonReply launches a follow-up of the chain's pull request and reads the
// reply back, as its members and as the raw document.
func jsonReply(t *testing.T, f *cloudFixture) (jsonLaunchReply, map[string]any, string) {
	t.Helper()
	out, errOut, err := execute(t, "run", "--repo", limitsRepo, "--pr", "7", "--run-id", jsonFu, "--json", "A task")
	if err != nil {
		t.Fatalf("run --pr: %v (%s)", err, errOut)
	}
	var res jsonLaunchReply
	var raw map[string]any
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("the reply is not one JSON object: %v\n%s", err, out)
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("the reply does not decode: %v\n%s", err, out)
	}
	return res, raw, out
}

// jsonRunsShow is `fugaro runs show --json` of id, decoded the way the reply is.
func jsonRunsShow(t *testing.T, id string) jsonLaunchReply {
	t.Helper()
	out, errOut, err := execute(t, "runs", "show", "--json", id)
	if err != nil {
		t.Fatalf("runs show --json %s: %v (%s)", id, err, errOut)
	}
	var show jsonLaunchReply
	if err := json.Unmarshal([]byte(out), &show); err != nil {
		t.Fatalf("runs show --json does not decode: %v\n%s", err, out)
	}
	return show
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
// the run that began it, how many runs of the chain the link reached — the run
// this launch started among them — whether the figures are the budget
// database's (D1046), and one row per dimension the chain's own limit bounds:
// the figures the caller needs to know what the run it just started may still
// spend, read from the chain behind the link it named and not from the run it
// named (D1047).
func TestLaunchJSONCarriesTheChain(t *testing.T) {
	f := chainRig(t)
	jsonChain(t, f)
	res, raw, out := jsonReply(t, f)

	if res.Status != "launched" || res.RunID != jsonFu {
		t.Fatalf("reply = %+v, want %s launched (%s)", res, jsonFu, out)
	}
	if res.Chain != jsonRoot {
		t.Errorf("chain = %q, want the run that began it (%s), not the run the launch followed up (%s)\n%s", res.Chain, jsonRoot, jsonTip, raw)
	}
	if res.RunCount != 4 || !res.LimitsRead {
		t.Errorf("reply = %+v, want the chain's three runs and the one this launch started, and its limit read", res)
	}
	if want := jsonLimitRows(false); !reflect.DeepEqual(res.Limits, want) {
		t.Errorf("limits = %+v, want %+v", res.Limits, want)
	}
}

// TestLaunchJSONLimitsMatchRunsShow: the reply and `runs show --json` of the run
// the launch started are one document written twice — the launch reply's
// members are the block that command prints (D1056), so a consumer that reads
// one and then the other is not left with two spellings of a figure or with a
// member one of them lacks. The chain is the one the run itself is in: its
// record names the run it continues, and the count is the same count on both
// sides, this launch's run among the chain's.
//
// The record is the one the runner writes as the run starts, so what the chain
// holds for it is nothing and the two reads are the same read: what the run
// spends from here on belongs to the chain, and to runs show, and not to a reply
// that was printed before it started spending.
func TestLaunchJSONLimitsMatchRunsShow(t *testing.T) {
	f := chainRig(t)
	jsonChain(t, f)
	res, _, _ := jsonReply(t, f)
	jsonStartedRun(t, f)

	show := jsonRunsShow(t, jsonFu)
	if res.Chain != show.Chain || res.RunCount != show.RunCount || res.Enforced != show.Enforced || res.LimitsRead != show.LimitsRead {
		t.Errorf("the reply says chain %q of %d runs (ENFORCED %v, read %v) where runs show of %s says chain %q of %d (ENFORCED %v, read %v)",
			res.Chain, res.RunCount, res.Enforced, res.LimitsRead, jsonFu, show.Chain, show.RunCount, show.Enforced, show.LimitsRead)
	}
	if want := jsonLimitRows(false); !reflect.DeepEqual(res.Limits, want) || !reflect.DeepEqual(show.Limits, want) {
		t.Errorf("limits = %+v and %+v, want %+v", res.Limits, show.Limits, want)
	}
}

// TestLaunchJSONLimitsAreTheLedgers (D1046): with the budget on, what the chain
// has spent is the budget database's, per run — reserved less released for the
// money and the ledger's token leaf — and the token dimension is measured here
// where a record states no token total at all.
//
// The rows are the chain the reply read, which is the chain behind the link it
// named: runs show of the run the launch continued reads the same three
// ledgers. The run the launch started is not among them — nothing writes its
// ledger until it starts spending — so what runs show of that run shares with
// the reply is the chain itself: the run that began it, how many runs it has,
// and whether its figures are the database's.
func TestLaunchJSONLimitsAreTheLedgers(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	limitsImage(t, f.cloudFixture)
	limitsCheckout(t)
	jsonChain(t, f.cloudFixture)
	for id, l := range map[string]budget.RunLedger{
		jsonRoot: {Reserved: 2_000_000, Released: 500_000, Tokens: 400_000},
		jsonMid:  {Reserved: 1_000_000, Released: 500_000, Tokens: 100_000},
		jsonTip:  {Reserved: 250_000, Released: 0, Tokens: 50_000},
	} {
		f.db.Set(budget.PathRun(appSlug, id), l)
	}
	res, _, _ := jsonReply(t, f.cloudFixture)
	jsonStartedRun(t, f.cloudFixture)

	want := jsonLimitRows(true)
	if !reflect.DeepEqual(res.Limits, want) {
		t.Errorf("limits = %+v, want the ledgers' figures, %+v", res.Limits, want)
	}
	if anchor := jsonRunsShow(t, jsonTip); !reflect.DeepEqual(anchor.Limits, want) {
		t.Errorf("runs show of %s reads %+v, want %+v", jsonTip, anchor.Limits, want)
	}
	show := jsonRunsShow(t, jsonFu)
	if res.Chain != show.Chain || res.RunCount != show.RunCount || res.Enforced != show.Enforced || res.LimitsRead != show.LimitsRead {
		t.Errorf("the reply says chain %q of %d runs (ENFORCED %v, read %v) where runs show of %s says chain %q of %d (ENFORCED %v, read %v)",
			res.Chain, res.RunCount, res.Enforced, res.LimitsRead, jsonFu, show.Chain, show.RunCount, show.Enforced, show.LimitsRead)
	}
}

// TestLaunchJSONCountsTheRunItLaunchedInAChainWithNoLink: a chain continued by
// naming the run --continues names has no record link — a stored task carries
// previous_run only with a branch and a pull request (task.Spec.Validate) — so
// the reply is the only document that can say how many runs the chain has, and
// it counts the run it just started among them. runs show of that run reads a
// chain of one, which is what its own record holds and not what the run is
// limited by (D1045, D1052).
func TestLaunchJSONCountsTheRunItLaunchedInAChainWithNoLink(t *testing.T) {
	f := chainRig(t)
	usd := chainUSD
	seedChainRun(t, f, chainRun, chainSpent, task.Overrides{MaxUSD: &usd})
	out, errOut, err := execute(t, "run", "--repo", limitsRepo, "--continues", chainRun, "--run-id", chainFu, "--json", "A task")
	if err != nil {
		t.Fatalf("run --continues: %v (%s)", err, errOut)
	}
	var res jsonLaunchReply
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("the reply does not decode: %v\n%s", err, out)
	}
	limit, used, left := runstore.Amount(5_000_000), runstore.Amount(2_000_000), runstore.Amount(3_000_000)
	want := []ChainLimit{{Dimension: runstore.USD, Limit: limit, Used: &used, Remaining: &left, Source: policy.SourceChain}}
	if res.Chain != chainRun || res.RunCount != 2 || !res.LimitsRead || !reflect.DeepEqual(res.Limits, want) {
		t.Errorf("reply = %+v, want chain %s of 2 runs (the run it continued and the one it started) with %+v\n%s", res, chainRun, want, out)
	}
}

// TestLaunchJSONNamesAChainItCouldNotFollow: a launch that joined a chain whose
// first run the bucket has aged out inside its own window cannot read the chain
// — the pull request's runs are there, the run their branch is named for is not
// — and it still carries the block, with an empty chain and no limit read. The
// members are what tells this reply apart from the reply of a launch that began
// a chain, whose block is absent: a consumer that read an absence here would
// read a run the chain bounds as one it does not (D1047).
func TestLaunchJSONNamesAChainItCouldNotFollow(t *testing.T) {
	f := newCloudFixture(t)
	jsonAgedChain(t, f)
	_, raw, out := jsonReply(t, f)

	for _, key := range []string{"chain", "run_count", "enforced", "limits_read", "limits"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("the reply of a launch that joined a chain the bucket cannot follow carries no %s:\n%s", key, out)
		}
	}
	var res jsonLaunchReply
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("the reply does not decode: %v\n%s", err, out)
	}
	if res.Chain != "" || res.LimitsRead {
		t.Errorf("reply = %+v, want an empty chain and no limit read rather than a chain named out of the runs the read did reach", res)
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
	res, _, _ := jsonReply(t, f)

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
	out, errOut, err := execute(t, "run", "--repo", limitsRepo, "--pr", "7", "--run-id", jsonFu, "--json", "A task")
	if err != nil {
		t.Fatalf("run --pr: %v (%s)", err, errOut)
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
