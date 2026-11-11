package bitbucket

import (
	"context"
	"fmt"

	"github.com/dimipaun/fugaro/internal/gitprov"
)

// The ladder's write calls on Bitbucket (E8). MarkReady works here, because a
// draft on this host is the title's own prefix and the adapter already knows
// how to drop it; Merge and Checks refuse, and each refusal is the honest
// answer to a question this host cannot be asked. The refusals are what the
// ladder's gate reads: a rung that reaches either ends waiting_on: person
// rather than merging or passing on a reading nobody made.

// MarkReady implements gitprov.Provider. Bitbucket has no draft flag on a
// pull request — the adapter marks one by the title's prefix
// (gitprov.DraftPrefix, the same fallback GitHub uses where drafts are
// unsupported) — so readying it is an ordinary update through the same code
// path UpdatePR uses, and it takes the provider's own credential: the gated
// merge mint is GitHub's (E16's route is an App's, D2049), and this host has
// no App to sit on a bypass list.
//
// The head is re-read and checked first, exactly as the GitHub adapter does
// it (G2): the draft state is a write, and a write that lands on a head other
// than the one the gate readied is the one thing this call exists to prevent.
func (p *Provider) MarkReady(ctx context.Context, number int, expectedHeadSHA string) error {
	info, err := p.PullRequest(ctx, number)
	if err != nil {
		return err
	}
	if info.State != gitprov.PROpen {
		return fmt.Errorf("pull request #%d is %s: %w", number, info.State, gitprov.ErrPRNotOpen)
	}
	if err := gitprov.CheckExpectedHead(info.HeadSHA, expectedHeadSHA); err != nil {
		return fmt.Errorf("marking pull request #%d ready: %w", number, err)
	}
	if !info.Draft {
		// Already ready: nothing to write, and no PUT that could fail on the
		// way (UpdatePR sends no write when nothing changes either).
		return nil
	}
	ready := false
	if _, err := p.UpdatePR(ctx, number, gitprov.PRUpdate{Draft: &ready}); err != nil {
		return fmt.Errorf("marking pull request #%d ready: %w", number, err)
	}
	return nil
}

// Merge implements gitprov.Provider by refusing: Bitbucket's merge endpoint
// takes no expected head, so a merge there is a merge at whatever commit the
// pull request is at when the call lands (F24). G2 forbids exactly that — the
// merge would land on a head nobody reviewed — so the ladder refuses here and
// raises waiting_on: person. The refusal is a sentinel rather than a plain
// error so the caller reports "this host cannot be merged this way" instead of
// retrying a call that can never succeed.
func (p *Provider) Merge(_ context.Context, number int, _ string) error {
	return fmt.Errorf("merging pull request #%d: %w", number, gitprov.ErrMergeNoHeadPrecondition)
}

// Checks implements gitprov.Provider by refusing: nothing in this adapter
// reads Bitbucket's pipeline or commit-status reports, so the adapter has no
// reading to give and says so rather than answering "no checks" — a state the
// caller reads as CIUnknown and would then have to tell apart from a real
// commit with no checks at all. A caller that wants the ladder to gate on a
// host other than GitHub needs a checks read built and tested for it, which is
// not this task's.
func (p *Provider) Checks(_ context.Context, number int, _ string) ([]gitprov.CheckRun, error) {
	return nil, fmt.Errorf("reading the checks of pull request #%d: %w", number, gitprov.ErrChecksUnsupported)
}
