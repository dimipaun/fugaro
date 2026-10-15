package cli

import (
	"strings"
	"testing"
)

// consoleContractPath is the console's JSON contract (design §12 item 1, plan
// Tasks 0.1 and 0.2). It is read as bytes and nothing else: this file imports
// nothing from under console/ and embeds nothing from it, which is what plan
// Task 0.3's boundary test asserts of the one entry in its allow-list — this
// file, the only Go file permitted to name a console/ path.
const consoleContractPath = "console/docs/json-contract.md"

// TestDocsDesktopConsoleContractSettlesTheFeed pins the consumption contract
// of `watch --json` — plan Task 0.2, settling the design's §12 item 2 by
// reading the CLI rather than assuming it. Each sub-assertion is a named fact
// and carries the symbol that proves it, so dropping a fact from the document
// fails a test named for it instead of quietly shrinking the contract. The
// four the plan names by hand are the slow reader, the closed stdout, the
// refusal and the UTC-midnight rollover; the rest are the same task's steps.
func TestDocsDesktopConsoleContractSettlesTheFeed(t *testing.T) {
	// Whitespace is normalised, as the sibling docs tests do: a sentence
	// rewrapped by a later edit is the same sentence.
	doc := strings.Join(strings.Fields(readDoc(t, consoleContractPath)), " ")
	for _, c := range []struct {
		fact string
		want []string
	}{
		{
			fact: "a slow reader blocks the child rather than dropping a document",
			want: []string{
				"A slow reader makes the child block; it never drops a document",
				"json.NewEncoder(d.Out).Encode",
				"Supervisor.emit",
				"watch.Start",
				"rtdb.Client.Stream",
				"c.idle",
				"backMin",
				"backMax",
				"a `put` of `\"/\"` holding the whole current value",
			},
		},
		{
			fact: "a closed stdout ends the child",
			want: []string{
				"A closed stdout ends the child",
				"SIGPIPE",
				"descriptors 1 and 2",
				"141",
				"watchStream",
				"ExitCode",
				"not wrapped with `remote()`",
				"ExitUserError",
			},
		},
		{
			fact: "a refused read is terminal and announced exactly once",
			want: []string{
				"A refused read is terminal, and it is announced exactly once",
				"State.conn",
				"ConnRefused",
				"refused",
				"dbErr(d.LC, rtdb.ErrPermission, false)",
				"phaseHalt",
				"ExitUserError",
				"Restarting the child before the identity or the role changes refuses again",
			},
		},
		{
			fact: "UTC midnight is a gap, not a zero",
			want: []string{
				"UTC midnight is a gap, not a zero",
				"budget.Day(s.now())",
				"streamPhase",
				"dayWG.Wait()",
				"rollDay",
				"openDay",
				"UpdDay",
				"SetDay",
				"rtdb.Tree.Reset",
				"BurnWarmup",
				"a document that is not a measurement",
			},
		},
		{
			fact: "the emission rule, and that a document is a sampled whole",
			want: []string{
				"refused || (ready && dirty && (last.IsZero() || time.Since(last) >= gap))",
				"watchJSONGap",
				"A document is a sampled whole, not a delta",
				"Silence means \"live and unchanged\", not \"the feed is dead\"",
				"A connection-state change is announced",
				"watch.StaleAfter",
			},
		},
		{
			fact: "the first document arrives within watchFirstFrame",
			want: []string{
				"The first document arrives within `watchFirstFrame`",
				"seen == [4]bool{true, true, true, true}",
				"watchFirstFrame",
				"Supervisor.run",
				"offline frame",
			},
		},
		{
			fact: "a dead or withdrawn stream is not an exit",
			want: []string{
				"A dead or withdrawn stream is not an exit",
				"probe",
				"pollPhase",
				"phaseTrial",
				"SSERetry",
				"the child stays alive",
			},
		},
		{
			fact: "the degraded mode is a second shape and a line that is not JSON",
			want: []string{
				"watchDegradedDoc",
				"degradedSince",
				"watchPlainInterval",
				"run records unavailable (retrying): %s",
				"skip any line that is not a JSON object",
				"printProjectHeader",
				"goes to stderr, which is not the document stream",
			},
		},
		{
			fact: "the thresholds a consumer that is not a terminal needs",
			want: []string{
				"watch.SilentAfter",
				"watch.LostAfter",
				"watch.BurnWindow",
				"watch.Options.Tick",
				"watch.Options.Interval",
				"degradedSince",
			},
		},
		{
			fact: "how many documents arrive per minute in each mode",
			want: []string{
				"How many documents arrive per minute, in each mode",
				"Live SSE (`watch --json`)",
				"Polling (`--poll`, or after three same-cause stream errors)",
				"Degraded (no `budget.rtdb_url`)",
				"exactly one, then exit",
			},
		},
		{
			fact: "option A needs a supervisor, and why",
			want: []string{
				"Does option A need a supervisor process? Yes",
				"The child exits on a refusal",
				"The child does not exit on a dead stream",
				"The child dies by `SIGPIPE` when the console's pipe closes",
				"one `watch --json` per selected installation",
			},
		},
		{
			fact: "the reader rules, including the narrowed repo list",
			want: []string{
				"Split stdout on newlines and decode each line as one JSON object",
				"A changed `day` is a break in the series, never a zero",
				"A missing field is `unavailable`, never `0`",
				"watch.FilterRepo",
				"Never `--follow`",
			},
		},
	} {
		t.Run(c.fact, func(t *testing.T) {
			for _, w := range c.want {
				if !strings.Contains(doc, strings.Join(strings.Fields(w), " ")) {
					t.Errorf("%s lacks %q (fact: %s)", consoleContractPath, w, c.fact)
				}
			}
		})
	}
}
