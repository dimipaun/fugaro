package bitbucket

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov"
)

// The E8 calls on this host: MarkReady works through the adapter's own draft
// handling, and Merge and Checks refuse. The two refusals are the point — the
// ladder reads them and ends a rung with waiting_on: person rather than
// merging, or passing on a CI reading nobody made.

const head = "1111111111111111111111111111111111111111"

// prJSONAt is prJSON with a source commit, which is what a head-bound call
// reads: Bitbucket reports the head under source.commit.hash.
func prJSONAt(state string, draft bool, title, desc, sha string) string {
	return fmt.Sprintf(`{"type": "pullrequest", "id": 12, "title": %q, "description": %q, "state": %q, "draft": %t, "reviewers": [], "source": {"branch": {"name": "fugaro/x"}, "commit": {"hash": %q}}, %s}`,
		title, desc, state, draft, sha, prLink)
}

// getPRAt is the one read every call here makes.
func getPRAt(state string, draft bool, title, desc, sha string) string {
	return `{"method": "GET", "path": "/2.0/repositories/acme/web/pullrequests/12", "status": 200, "response": ` + prJSONAt(state, draft, title, desc, sha) + `}`
}

// TestBitbucketMergeRefusesWithoutHeadPrecondition is E8's refusal for the host
// whose merge API has no expected-head precondition (F24): nothing is sent, and
// the caller can tell this refusal from an outage. The provider is opened with
// no exchange at all, so a merge that reached out would fail on the fixture
// rather than pass.
func TestBitbucketMergeRefusesWithoutHeadPrecondition(t *testing.T) {
	p := openInline(t, nil)
	err := p.Merge(ctx, 12, head)
	if !errors.Is(err, gitprov.ErrMergeNoHeadPrecondition) {
		t.Fatalf("Merge = %v, want ErrMergeNoHeadPrecondition", err)
	}
	if !strings.Contains(err.Error(), "#12") {
		t.Fatalf("the refusal does not name the pull request: %v", err)
	}
}

// TestBitbucketChecksRefuses pins the other refusal: this adapter reads no
// checks, and "I cannot read them" is not "there are none" — the caller sees an
// error, never a state it could take for green.
func TestBitbucketChecksRefuses(t *testing.T) {
	p := openInline(t, nil)
	runs, err := p.Checks(ctx, 12, head)
	if !errors.Is(err, gitprov.ErrChecksUnsupported) || runs != nil {
		t.Fatalf("Checks = %+v, %v; want ErrChecksUnsupported and no runs", runs, err)
	}
}

// TestBitbucketMarkReadyRefusesOnHeadChange is MarkReady's half of G2 on this
// host: a draft is readied only at the head the call named, and a pull request
// at another commit is left alone — the fixture holds one GET and no PUT, so a
// write here would fail the test.
func TestBitbucketMarkReadyRefusesOnHeadChange(t *testing.T) {
	p := openInline(t, nil, getPRAt("OPEN", true, gitprov.DraftPrefix+"Add search", "Adds search.", head))
	err := p.MarkReady(ctx, 12, "2222222222222222222222222222222222222222")
	if !errors.Is(err, gitprov.ErrHeadMoved) {
		t.Fatalf("MarkReady at a moved head = %v, want ErrHeadMoved", err)
	}
}

// TestBitbucketMarkReadyClearsTheDraftPrefix covers the call working as F24
// describes it — E8 wraps the adapter's own draft handling — on the fallback
// this host uses: the title's prefix is what makes the pull request a draft,
// and readying it strips the prefix.
func TestBitbucketMarkReadyClearsTheDraftPrefix(t *testing.T) {
	p := openInline(t, nil,
		getPRAt("OPEN", true, gitprov.DraftPrefix+"Add search", "Adds search.", head),
		getPRAt("OPEN", true, gitprov.DraftPrefix+"Add search", "Adds search.", head),
		put(`{"title": "Add search", "draft": false}`, prJSONAt("OPEN", false, "Add search", "Adds search.", head)))
	if err := p.MarkReady(ctx, 12, head); err != nil {
		t.Fatal(err)
	}
}

// TestBitbucketMarkReadyOnAReadyPRWritesNothing covers idempotence: a pull
// request that is already ready is not written again. The fixture holds the one
// GET and nothing else, so a PUT would fail the test.
func TestBitbucketMarkReadyOnAReadyPRWritesNothing(t *testing.T) {
	p := openInline(t, nil, getPRAt("OPEN", false, "Add search", "Adds search.", head))
	if err := p.MarkReady(ctx, 12, head); err != nil {
		t.Fatal(err)
	}
}

// TestBitbucketMarkReadyRefusesAClosedPR keeps the write on the same footing as
// every other write in this adapter.
func TestBitbucketMarkReadyRefusesAClosedPR(t *testing.T) {
	p := openInline(t, nil, getPRAt("MERGED", true, gitprov.DraftPrefix+"Add search", "Adds search.", head))
	if err := p.MarkReady(ctx, 12, head); !errors.Is(err, gitprov.ErrPRNotOpen) {
		t.Fatalf("MarkReady on a merged pull request = %v, want ErrPRNotOpen", err)
	}
}

// TestBitbucketSaysItIsExemptFromTheMergeMint is the exception made readable:
// MarkReady here takes the provider's own credential rather than the gated
// merge mint's, and the gate is told so instead of having to infer it from
// prose. The exemption covers the credential only — the merge still refuses
// below — so an exempt provider is not a provider that can merge unbound.
func TestBitbucketSaysItIsExemptFromTheMergeMint(t *testing.T) {
	var prov gitprov.Provider = openInline(t, nil)
	exempt, ok := prov.(gitprov.MintExempt)
	if !ok {
		t.Fatal("the Bitbucket provider does not report itself exempt from the merge mint")
	}
	if err := exempt.MergeMintExemption(); !errors.Is(err, gitprov.ErrMintNotApplicable) {
		t.Fatalf("MergeMintExemption = %v, want ErrMintNotApplicable", err)
	}
	if _, ok := prov.(gitprov.MergeMintSetter); ok {
		t.Fatal("the Bitbucket provider offers to install a merge mint it has no use for")
	}
	if err := prov.Merge(ctx, 12, head); !errors.Is(err, gitprov.ErrMergeNoHeadPrecondition) {
		t.Fatalf("Merge on an exempt provider = %v, want it still refused", err)
	}
}
