// Package gitprov abstracts the git hosting provider (design §3.1). Real
// GitHub and Bitbucket implementations arrive in M2.
package gitprov

import "context"

// PRSpec describes the pull request a run wants.
type PRSpec struct {
	Branch    string   `json:"branch"`
	Base      string   `json:"base"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	Draft     bool     `json:"draft"`
	Labels    []string `json:"labels,omitempty"`
	Reviewers []string `json:"reviewers,omitempty"`
}

// PR is a pull request on the provider.
type PR struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	Draft  bool   `json:"draft"`
}

// Provider is what the runner needs from the git host.
type Provider interface {
	// EnsurePR creates the pull request for spec.Branch or, if one already
	// exists (the agent may have opened it), updates only its draft state.
	EnsurePR(ctx context.Context, spec PRSpec) (PR, error)
	// Comment posts a comment on the pull request.
	Comment(ctx context.Context, pr PR, body string) error
}
