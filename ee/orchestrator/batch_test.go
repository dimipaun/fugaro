// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package orchestrator_test

// The shared half's batch rule: how many work items one approval covers in a
// project, which is the spec's "Batch approval | Project setting" row read
// through D1239's answer — the shipped default is ten and not the spec's fifty,
// a person is who raises it, and no approval ever widens the project's own cap.
//
// What every case hands in is the project's own setting and the class of the
// actor (internal/orchestrator.Batch), the way the handoff cases hand in the
// resolver's input: the rule is a pure function of the value, so the ceiling a
// project that raised its batch would resolve is a value a test can state
// without a cloud, and a class that is not one of the four is a value too.
//
// This test binary links internal/orchestrator and ee/orchestrator and NOT
// ee/register, which is what installs the rule in the core's seam — so what the
// last test here exercises is the build without ee/, exactly as the handoff
// suite does for its own seam.

import (
	"errors"
	"testing"

	eeorch "github.com/dimipaun/fugaro/ee/orchestrator"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/policy"
)

// TestBatchDefaultIsTenAndOnlyAPersonRaisesIt is O18's red step and D1239's
// answer over one approval: the batch is the ten Fugaro ships unless the project
// set its own number, and the spec's fifty is never what a build answers by
// itself. Only a person raises one approval above the shipped ten — the class G2
// already fences at the CLI with the Typed confirmation, so a raise costs the
// same confirmation the approval it lands with does — and every other class,
// including one this tree does not spell, gets the shipped default or a refusal.
func TestBatchDefaultIsTenAndOnlyAPersonRaisesIt(t *testing.T) {
	for _, tc := range []struct {
		name string
		b    orchestrator.Batch
		want int64
		ok   bool
	}{
		{"nobody asked", orchestrator.Batch{By: orchestrator.Person}, policy.DefaultBatchSize, true},
		{"nobody asked, the project set its own number",
			orchestrator.Batch{ProjectCap: 25, By: orchestrator.Person}, 25, true},
		{"nobody asked, the project tightened it",
			orchestrator.Batch{ProjectCap: 3, By: orchestrator.Person}, 3, true},
		{"a person asks for the shipped ten", orchestrator.Batch{Covered: 10, By: orchestrator.Person}, 10, true},
		{"a person asks for fewer than the ten", orchestrator.Batch{Covered: 4, By: orchestrator.Person}, 4, true},
		{"a person raises the project's own number",
			orchestrator.Batch{Covered: 25, ProjectCap: 25, By: orchestrator.Person}, 25, true},
		{"the daemon asks for more than the ten",
			orchestrator.Batch{Covered: 25, ProjectCap: 25, By: orchestrator.Daemon}, 0, false},
		{"a session asks for more than the ten",
			orchestrator.Batch{Covered: 11, ProjectCap: 11, By: orchestrator.Session}, 0, false},
		{"the decision agent asks for more than the ten",
			orchestrator.Batch{Covered: 11, ProjectCap: 11, By: orchestrator.DecisionAgent}, 0, false},
		{"a class this tree does not spell asks for more than the ten",
			orchestrator.Batch{Covered: 11, ProjectCap: 11, By: orchestrator.Class("launcher")}, 0, false},
		{"the daemon asks for the shipped ten",
			orchestrator.Batch{Covered: 10, By: orchestrator.Daemon}, 10, true},
		{"nobody asked and the daemon reads the project's raised number",
			orchestrator.Batch{ProjectCap: 25, By: orchestrator.Daemon}, 0, false},
		{"nobody asked and a session reads the project's raised number",
			orchestrator.Batch{ProjectCap: 25, By: orchestrator.Session}, 0, false},
		{"the decision agent reads the project's raised number",
			orchestrator.Batch{Covered: 25, ProjectCap: 25, By: orchestrator.DecisionAgent}, 0, false},
		{"a batch of a negative number of work items",
			orchestrator.Batch{Covered: -1, ProjectCap: 25, By: orchestrator.Person}, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := eeorch.BatchSize(tc.b)
			if !tc.ok {
				if !errors.Is(err, eeorch.ErrBatchRefused) {
					t.Fatalf("BatchSize answered %d, %v, want a refusal: only a person raises one approval above the %d work items Fugaro ships", got, err, policy.DefaultBatchSize)
				}
				if got != 0 {
					t.Errorf("a refused batch answered %d work items, want none: a caller reading a number off a refused call would approve a batch nothing allowed", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("BatchSize: %v", err)
			}
			if got != tc.want {
				t.Errorf("BatchSize answered %d work items, want %d", got, tc.want)
			}
		})
	}
}

// TestBatchNeverWidensTheProjectCap is the other half of D1239's tightening
// rule: the project's own setting is the ceiling, and an approval that named
// more work items than it would widen it is refused for everyone, the person
// included. A project that wrote no batch is at the shipped ten, so an approval
// above ten is refused there too — "the project set nothing" is not "the project
// allows anything" — and a project that tightened its batch below the shipped
// one is the ceiling its approvals are held to.
func TestBatchNeverWidensTheProjectCap(t *testing.T) {
	for _, tc := range []struct {
		name string
		b    orchestrator.Batch
		want int64
		ok   bool
	}{
		{"a person asks for more than the project's twenty", orchestrator.Batch{Covered: 25, ProjectCap: 20, By: orchestrator.Person}, 0, false},
		{"a person asks for more than a project that set nothing", orchestrator.Batch{Covered: 25, By: orchestrator.Person}, 0, false},
		{"a person asks for one more than the shipped ten", orchestrator.Batch{Covered: 11, By: orchestrator.Person}, 0, false},
		{"a person asks for more than a project tightened to three", orchestrator.Batch{Covered: 5, ProjectCap: 3, By: orchestrator.Person}, 0, false},
		{"a person asks for exactly the project's cap", orchestrator.Batch{Covered: 20, ProjectCap: 20, By: orchestrator.Person}, 20, true},
		{"a person asks for exactly a project's tightened three", orchestrator.Batch{Covered: 3, ProjectCap: 3, By: orchestrator.Person}, 3, true},
		{"the daemon asks for more than the project's cap", orchestrator.Batch{Covered: 25, ProjectCap: 20, By: orchestrator.Daemon}, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := eeorch.BatchSize(tc.b)
			if !tc.ok {
				if !errors.Is(err, eeorch.ErrBatchRefused) {
					t.Fatalf("BatchSize answered %d, %v, want a refusal: an approval never widens the project's own cap", got, err)
				}
				if got != 0 {
					t.Errorf("a refused batch answered %d work items, want none", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("BatchSize: %v", err)
			}
			if got != tc.want {
				t.Errorf("BatchSize answered %d work items, want %d", got, tc.want)
			}
		})
	}
}

// TestBuildWithoutEEKeepsTheBatchAtTheShippedDefault is the core seam's own
// half, read through internal/orchestrator because this binary does not link
// ee/register: a build without the shared half carries no raise at all, so a
// batch above the shipped ten is refused rather than quietly cut to ten — a
// caller that was answered ten for a request of twenty-five would approve ten
// work items believing it had approved twenty-five — and a project that carries
// a raised setting of its own is answered with the ten as well, because that
// setting is a raise and this build has no rule to make one.
func TestBuildWithoutEEKeepsTheBatchAtTheShippedDefault(t *testing.T) {
	for _, tc := range []struct {
		name string
		b    orchestrator.Batch
		want int64
		ok   bool
	}{
		{"nobody asked", orchestrator.Batch{By: orchestrator.Person}, policy.DefaultBatchSize, true},
		{"a project that tightened its batch", orchestrator.Batch{ProjectCap: 4, By: orchestrator.Person}, 4, true},
		{"nobody asked and the project carries a raised setting", orchestrator.Batch{ProjectCap: 25, By: orchestrator.Person}, policy.DefaultBatchSize, true},
		{"an approval of twenty-five work items", orchestrator.Batch{Covered: 25, ProjectCap: 25, By: orchestrator.Person}, 0, false},
		{"an approval above a project's tightened four", orchestrator.Batch{Covered: 5, ProjectCap: 4, By: orchestrator.Person}, 0, false},
		{"a batch of a negative number of work items", orchestrator.Batch{Covered: -1, By: orchestrator.Person}, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := orchestrator.ApprovalBatch(tc.b)
			if !tc.ok {
				if !errors.Is(err, orchestrator.ErrActorNotAllowed) {
					t.Fatalf("a build without ee/ answered %d, %v, want the core's refusal: the raise is the shared half's", got, err)
				}
				if got != 0 {
					t.Errorf("a refused batch answered %d work items, want none", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ApprovalBatch: %v", err)
			}
			if got != tc.want {
				t.Errorf("a build without ee/ answers %d work items, want %d", got, tc.want)
			}
		})
	}
}
