package github

import (
	"context"
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
)

// The ladder's write calls (E8): marking a pull request ready and merging it,
// each bound to one head and each made with the gated merge mint's credential
// (D2049). Neither call sends an admin or bypass flag, and neither can be made
// without the mint: the token these two take is the one the gate bound to a
// head, so a provider that quietly used its own installation token instead
// would be a merge outside the gate (G14).

// mergeMint is where this provider's ready and merge credentials come from,
// installed once before the daemon's first call (gitprov.MergeMintSetter).
var _ gitprov.MergeMintSetter = (*Provider)(nil)

func (p *Provider) UseMergeMint(m gitprov.MergeMinter) {
	if m == nil {
		return
	}
	p.mergeMint = m
}

// mergeToken mints the credential for one head-bound call and returns the
// client to make it with.
//
// The mint is asked for the head the caller named, and it checks that head
// against GitHub's own answer before it hands a token back (E16): a head that
// moved between the caller's read and this mint is refused here, which is the
// first of the two places G2 is enforced. The second is the `sha` this file
// sends to the merge endpoint.
func (p *Provider) mergeToken(ctx context.Context, number int, expectedHeadSHA string) (*httpjson.Client, error) {
	if p.mergeMint == nil {
		return nil, fmt.Errorf("pull request #%d: %w", number, gitprov.ErrNoMergeMint)
	}
	token, _, err := p.mergeMint.MintMerge(ctx, p.owner+"/"+p.repo, number, expectedHeadSHA)
	if err != nil {
		return nil, fmt.Errorf("no merge credential for pull request #%d: %w", number, err)
	}
	if token == "" {
		return nil, fmt.Errorf("no merge credential for pull request #%d: the mint returned no token", number)
	}
	return p.withToken(token), nil
}

// withToken is a copy of this provider's API client that authenticates with
// token instead of p.tokens. It is a copy rather than a second client so the
// base URL, the HTTP client and the headers stay the ones the provider was
// built with, and it is per call rather than stored so a minted token is never
// reused for another call.
func (p *Provider) withToken(token string) *httpjson.Client {
	c := *p.api
	c.Auth = func(context.Context) (string, error) { return "Bearer " + token, nil }
	return &c
}

// MarkReady implements gitprov.Provider (E8, D2041): the daemon's authority to
// ready a draft a run left stalled, and only at the head the gate read.
//
// It is not ApplyReady. ApplyReady adds reviewers and labels to a pull request
// somebody else made ready, and it cannot un-draft anything; this call is the
// un-draft itself, through the same code path UpdatePR uses for it (F24) —
// GitHub's GraphQL markPullRequestReadyForReview, or the title prefix where
// the repository's plan has no draft pull requests at all.
func (p *Provider) MarkReady(ctx context.Context, number int, expectedHeadSHA string) error {
	c, err := p.mergeToken(ctx, number, expectedHeadSHA)
	if err != nil {
		return err
	}
	d, info, err := p.getPullWith(ctx, c, number)
	if err != nil {
		return err
	}
	// The state is checked before the head, the way UpdatePR and ApplyReady
	// check it and the way Bitbucket's MarkReady does: a closed pull request
	// keeps its head, so the head check would pass and the write below — at
	// minimum the REST retitle a prefix-marked draft gets — would land on a
	// pull request that is closed. The head is not the question for a closed
	// one; the answer is the refusal this provider's every other write gives.
	if info.State != gitprov.PROpen {
		return fmt.Errorf("pull request #%d is %s: %w", info.Number, info.State, gitprov.ErrPRNotOpen)
	}
	// The head is re-read with the minted credential and checked here as well
	// as at the mint: the mint's read and this one are two answers from GitHub
	// about the same pull request, and a push landing between them is exactly
	// what the second read catches.
	if err := gitprov.CheckExpectedHead(d.Head.SHA, expectedHeadSHA); err != nil {
		return fmt.Errorf("marking pull request #%d ready: %w", number, err)
	}
	if !d.Draft && !strings.HasPrefix(d.Title, gitprov.DraftPrefix) {
		// Already ready: nothing to write, and no write that could fail.
		return nil
	}
	if _, err := p.update(ctx, c, d.pull, false); err != nil {
		return fmt.Errorf("marking pull request #%d ready: %w", number, err)
	}
	return nil
}

// Merge implements gitprov.Provider (E8, G2, G14): pull request number is
// merged at exactly expectedHeadSHA or not at all.
//
// The body carries the expected head and nothing else. GitHub's merge endpoint
// also takes a merge_method and a commit message, and this call sends neither:
// the method is the repository's own default (a repository that allows one
// method gets that one), and a message Fugaro wrote would be a message about a
// merge it is not making on anybody's behalf. What it must never carry is a
// bypass: there is no admin flag, no `force`, no protection override, and no
// `sha` other than the head the gate admitted.
func (p *Provider) Merge(ctx context.Context, number int, expectedHeadSHA string) error {
	c, err := p.mergeToken(ctx, number, expectedHeadSHA)
	if err != nil {
		return err
	}
	d, info, err := p.getPullWith(ctx, c, number)
	if err != nil {
		return err
	}
	// A closed pull request keeps its head, so the head check below would pass
	// on one a person already merged or declined and this call would send a
	// merge at it anyway; whatever the endpoint answers then is a sentence
	// about a call that should never have been made. The state is the
	// refusal, and it is checked first, as UpdatePR, ApplyReady and
	// Bitbucket's MarkReady check it.
	if info.State != gitprov.PROpen {
		return fmt.Errorf("pull request #%d is %s: %w", info.Number, info.State, gitprov.ErrPRNotOpen)
	}
	if err := gitprov.CheckExpectedHead(d.Head.SHA, expectedHeadSHA); err != nil {
		return fmt.Errorf("merging pull request #%d: %w", number, err)
	}
	var out struct {
		Merged  bool   `json:"merged"`
		Message string `json:"message"`
	}
	body := map[string]any{"sha": expectedHeadSHA}
	if err := c.Do(ctx, "PUT", p.repoPath(fmt.Sprintf("/pulls/%d/merge", number)), body, &out); err != nil {
		return fmt.Errorf("merging pull request #%d: %w", number, err)
	}
	if !out.Merged {
		// A 200 with merged false is GitHub answering "not merged" (a merge
		// commit it could not create, a base that requires something the pull
		// request does not have): the caller must not read it as done.
		return fmt.Errorf("merging pull request #%d: GitHub did not merge it: %s", number, orGitHubMessage(out.Message))
	}
	return nil
}

// orGitHubMessage keeps a refusal readable when GitHub sends no message with
// it, rather than ending the sentence in a colon.
func orGitHubMessage(msg string) string {
	if strings.TrimSpace(msg) == "" {
		return "it answered without a reason"
	}
	return msg
}
