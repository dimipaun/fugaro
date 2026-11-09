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
// The three answers, in the order they are asked:
//
//   - Nobody asked (Covered is zero) answers the project's own setting, which is
//     the ten Fugaro ships when the project wrote none. A project that tightened
//     its batch below the ten is answered with its own number, because the
//     tightened one is what its approvals are held to.
//   - Above the ceiling is refused, for everyone, the person included: a batch
//     approval that named more work items than the project's own setting would
//     widen a guardrail the project set (D1239's tightening rule, G7), and an
//     approval is not a way around the caps a cycle spawns under.
//   - Above the shipped ten, but within the project's ceiling, is a person's
//     decision and nobody else's. The daemon, the session and the decision agent
//     all act with nobody watching, so a batch one of them raised is a batch
//     nobody read; a person is the class G2 already fences with the Typed
//     confirmation at a terminal, so the raise costs the same confirmation the
//     approval it lands with does.
//
// The class is read as a value and never as a role: Person is the tree's own
// name for the person at a terminal, and a class this tree does not spell
// (internal/orchestrator.Classes) is not a person, so it never raises a batch —
// what it may cover is the shipped default, like the daemon.
//
// What this does not do is count the work items a document names: the batch is
// the number the caller resolved, and whether the items exist, are in the right
// phase or are approved one by one is the state model's business (O0), which
// reads the document it lands.
func BatchSize(b orchestrator.Batch) (int64, error) {
	ceiling := b.ProjectCap
	if ceiling <= 0 {
		ceiling = policy.DefaultBatchSize
	}
	if b.Covered <= 0 {
		return ceiling, nil
	}
	if b.Covered > ceiling {
		return 0, fmt.Errorf("%w: %d work items in one approval is above the project's cap of %d, and a batch never widens it",
			ErrBatchRefused, b.Covered, ceiling)
	}
	if b.Covered > policy.DefaultBatchSize && b.By != orchestrator.Person {
		return 0, fmt.Errorf("%w: %d work items in one approval is above the %d Fugaro ships, and only a person raises a batch",
			ErrBatchRefused, b.Covered, policy.DefaultBatchSize)
	}
	return b.Covered, nil
}
