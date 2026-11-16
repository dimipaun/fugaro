// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package orchestrator_test

// The shared half's workspace rule: whether a run the daemon launched came back
// refused by the workspace its credentials came from (O19, D2022 (a)).
//
// The rule is a predicate over one run's report and nothing else — no database,
// no network, no clock — and what it is asked to do is tell a refusal the
// workspace's own credentials service answered from an ordinary outage that
// happens to be spelled the same way. The marker alone cannot do it: the
// marker is written into the run's own record, and the run's job account writes
// that record (SECURITY.md, "Runs of one repository can see each other"), so a
// hostile run can claim a refusal no workspace's service made. What the rule
// adds is the second half — the halt beside the marker has to be the halt that
// path writes — which costs a forger the shape of a refusal, and the pause the
// claim still earns is bounded in time by the core
// (orchestrator.WorkspacePauseWindow).
//
// This test binary links internal/orchestrator and ee/orchestrator and NOT
// ee/register, so the last case here is the build without ee/ as the handoff
// and batch suites exercise theirs.

import (
	"testing"

	eeorch "github.com/dimipaun/fugaro/ee/orchestrator"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// workspaceHalt is the halt a run the workspace refused is written with, as the
// gateway writes it: the one spelling of the shape, read from the package that
// writes it rather than restated here.
func workspaceHalt() orchestrator.Halt {
	reason, scope, detail := gateway.WorkspaceRefusalHalt()
	return orchestrator.Halt{Reason: reason, Scope: scope, Detail: detail}
}

// TestWorkspaceRefusedReadsOnlyTheRunRecord pins the rule's whole table: which
// reports it claims, and — the half that matters — which it refuses.
//
// A marker with the halt that path writes is a workspace's refusal. A marker
// with anything else is not: another reason, another scope, another sentence,
// or no halt at all is a record that says "the workspace refused me" and
// nothing behind it, which is the accident the field exists to prevent and the
// cheapest thing a forger would try. A halt with no marker is a member's own
// service outage, which is the same halt and not a workspace's refusal. And a
// marker this release does not know is not a refusal this contract could have
// written, so it is answered false rather than compared.
//
// The reader is the whole input: every case is one report, and the rule reads
// nothing else, which is what lets a daemon answer it inside a fold.
func TestWorkspaceRefusedReadsOnlyTheRunRecord(t *testing.T) {
	reason, scope, detail := gateway.WorkspaceRefusalHalt()
	halt := func(r, s, d string) orchestrator.Halt { return orchestrator.Halt{Reason: r, Scope: s, Detail: d} }
	refused := func(marker string, h orchestrator.Halt) orchestrator.RunReport {
		return orchestrator.RunReport{
			RunID: "20261107-090000-abcd", Started: true, Finished: true,
			Outcome: orchestrator.OutcomeRefused, WorkspaceRefusal: marker, Halt: h,
		}
	}

	for _, tc := range []struct {
		name string
		rep  orchestrator.RunReport
		want bool
	}{
		{"the workspace's stop, with the halt that path writes", refused(gateway.WorkspaceRefusalStop, workspaceHalt()), true},
		{"the workspace's cap, with the halt that path writes", refused(gateway.WorkspaceRefusalCap, workspaceHalt()), true},
		{"a settle the service could not write back", refused(gateway.WorkspaceRefusalSettle, workspaceHalt()), true},

		{"a marker with no halt behind it", refused(gateway.WorkspaceRefusalStop, orchestrator.Halt{}), false},
		{"a marker with another reason", refused(gateway.WorkspaceRefusalStop, halt("budget_exhausted", scope, detail)), false},
		{"a marker with another scope", refused(gateway.WorkspaceRefusalStop, halt(reason, "item", detail)), false},
		{"a marker with another sentence", refused(gateway.WorkspaceRefusalStop, halt(reason, scope, "the workspace refused this run")), false},
		{"a marker with a halt that carries no reason", refused(gateway.WorkspaceRefusalStop, halt("", scope, detail)), false},
		{"a marker with a halt that carries no detail", refused(gateway.WorkspaceRefusalStop, halt(reason, scope, "")), false},

		{"a halt with no marker: a member's own service outage", refused("", workspaceHalt()), false},
		{"a marker this release does not know", refused("stopped", workspaceHalt()), false},
		{"a marker in another case", refused("STOP", workspaceHalt()), false},
		// A run still going is a run whose report says so, and whether one is
		// finished at all is the fold's question and not this rule's: it is
		// asked about the record, and a record carrying a refusal is a record
		// carrying a refusal (tick.go's fold asks it only of a finished run).
		{"a marker on a run the report calls unfinished", orchestrator.RunReport{
			RunID: "20261107-090000-abcd", Started: true, WorkspaceRefusal: gateway.WorkspaceRefusalStop, Halt: workspaceHalt(),
		}, true},
		{"an ordinary failed run", orchestrator.RunReport{
			RunID: "20261107-090000-abcd", Started: true, Finished: true, Outcome: orchestrator.OutcomeFailed,
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := eeorch.WorkspaceRefused(tc.rep); got != tc.want {
				t.Errorf("WorkspaceRefused answered %v for %+v, want %v", got, tc.rep, tc.want)
			}
		})
	}
}

// TestTheWorkspaceRuleReadsNothingButTheReport pins the "only the run record"
// half as a property rather than a promise: the same report answers the same
// thing however many times it is asked and whatever else the process has done,
// which is what a rule that reached a database, a network or a clock could not
// say. It is also what makes the rule safe to call inside a fold, where it runs
// once per finished attempt in a cycle that has already read everything it is
// going to read.
func TestTheWorkspaceRuleReadsNothingButTheReport(t *testing.T) {
	rep := orchestrator.RunReport{
		RunID: "20261107-090000-abcd", Started: true, Finished: true,
		Outcome: orchestrator.OutcomeRefused, WorkspaceRefusal: gateway.WorkspaceRefusalStop, Halt: workspaceHalt(),
	}
	first := eeorch.WorkspaceRefused(rep)
	for range 8 {
		if got := eeorch.WorkspaceRefused(rep); got != first {
			t.Fatalf("the same report answered %v and then %v: the rule is not a function of the record alone", first, got)
		}
	}
	// The report is the caller's value and the rule does not keep it: a caller
	// that reuses one for the next run cannot have it answer the last one's
	// refusal.
	other := rep
	other.WorkspaceRefusal = ""
	if eeorch.WorkspaceRefused(other) {
		t.Error("a report with the marker removed still reads as a workspace's refusal, so the answer is remembered rather than read")
	}
	if !eeorch.WorkspaceRefused(rep) {
		t.Error("the first report stopped reading as a refusal after another one was asked about")
	}
}

// TestTheUntaggedBuildClaimsNoRefusal is this binary's half of the boundary:
// it links the core and the feature and not the registration, so the core's
// workspace seam is the one an untagged build has — and that default answers
// false for every report, this build's own rule included. A queue paused on a
// claim nothing checked is worse than a queue that retries, so the direction
// the default fails in is the safe one.
func TestTheUntaggedBuildClaimsNoRefusal(t *testing.T) {
	if orchestrator.WorkspaceRefused(orchestrator.RunReport{
		RunID: "20261107-090000-abcd", Started: true, Finished: true,
		Outcome: orchestrator.OutcomeRefused, WorkspaceRefusal: gateway.WorkspaceRefusalStop, Halt: workspaceHalt(),
	}) {
		t.Error("a build that did not link ee/register claimed a run was refused by a workspace: the rule that reads the marker is the shared half's, and without it nothing may pause the queue")
	}
}
