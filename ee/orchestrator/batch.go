// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package orchestrator

import (
	"errors"
	"fmt"

	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/policy"
)

// ErrBatchRefused is a batch this package turned down: the approval would widen
// the project's cap, or the actor asking is not a person and would raise one
// above the number Fugaro ships. It is a value so a caller can tell a refusal
// from a failure to read the project's setting — the first is this tree's
// answer, the second is a re-read.
var ErrBatchRefused = errors.New("ee/orchestrator: this batch approval is not one this project allows")

// BatchSize answers how many work items one approval covers in a project, or
// the refusal that stops it. It is the implementation ee/register installs in
// the core's batch seam (internal/orchestrator.SetBatch), and the whole of the
// rule the spec's guardrail table states for batch approval — "Project setting,
// default 50 tasks in one batch; can be raised or disabled" — as D1239 answers
// it: the default a build ships is ten and not fifty, a person is who raises it,
// and the project's own setting is a cap no approval widens.
//
// The answers, in the order they are asked:
//
//   - A negative Covered is refused: a batch is a count of work items, and a
//     count below zero is not one — the same refusal the policy makes of its own
//     guardrail counts. Reading it as "nobody said" would answer a malformed
//     request with the largest batch the project allows.
//   - Above the ceiling is refused, for everyone, the person included: a batch
//     approval that named more work items than the project's own setting would
//     widen a guardrail the project set (D1239's tightening rule, G7), and an
//     approval is not a way around the caps a cycle spawns under.
//   - Nobody asked (Covered is zero) answers the project's own setting, which is
//     the ten Fugaro ships when the project wrote none. A project that tightened
//     its batch below the ten is answered with its own number, because the
//     tightened one is what its approvals are held to.
//   - Above the shipped ten is a person's decision and nobody else's — whether
//     the number was asked for or is the project's own setting, because a
//     project's raised setting is a person's decision that a class acting with
//     nobody watching reads rather than makes. The daemon, the session and the
//     decision agent all act with nobody watching; a person is the class G2
//     already fences with the Typed confirmation at a terminal, so the raise
//     costs the same confirmation the approval it lands with does.
//
// The class is read as a value and never as a role: Person is the tree's own
// name for the person at a terminal, and a class this tree does not spell
// (internal/orchestrator.Classes) is not a person, so it is held to the shipped
// ten like the daemon.
//
// What this does not do is count the work items a document names: the batch is
// the number the caller resolved, and whether the items exist, are in the right
// phase or are approved one by one is the state model's business (O0), which
// reads the document it lands.
func BatchSize(b orchestrator.Batch) (int64, error) {
	if b.Covered < 0 {
		return 0, fmt.Errorf("%w: %d is not a number of work items: a batch is a count of them and cannot be negative, and a batch left out takes the project's own setting",
			ErrBatchRefused, b.Covered)
	}
	ceiling := b.ProjectCap
	if ceiling <= 0 {
		ceiling = policy.DefaultBatchSize
	}
	if b.Covered > ceiling {
		return 0, fmt.Errorf("%w: %d work items in one approval is above the project's cap of %d, and a batch never widens it",
			ErrBatchRefused, b.Covered, ceiling)
	}
	covered := b.Covered
	if covered == 0 {
		covered = ceiling
	}
	if covered > policy.DefaultBatchSize && b.By != orchestrator.Person {
		return 0, fmt.Errorf("%w: %d work items in one approval is above the %d Fugaro ships, and only a person raises a batch",
			ErrBatchRefused, covered, policy.DefaultBatchSize)
	}
	return covered, nil
}
