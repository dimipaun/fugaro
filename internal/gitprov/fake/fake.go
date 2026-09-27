// Package fake is an in-memory gitprov.Provider, optionally persisted to a
// JSON file so separate processes (tests, `fugaro exec --provider fake`) share it.
package fake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
)

// PRState is a pull request and everything done to it.
type PRState struct {
	gitprov.PR
	Spec     gitprov.PRSpec `json:"spec"`
	Comments []string       `json:"comments"`
}

// State is the fake provider's data.
type State struct {
	PRs []PRState `json:"prs"`
}

// Provider is a fake git host.
type Provider struct {
	Path string // optional; when set, State is loaded and saved on every call
	// Auth, when set, supplies GitAuth's result for each minValid asked
	// for. When nil, GitAuth returns no credentials (local remotes need none).
	Auth func(minValid time.Duration) gitprov.GitAuth
	// FailEnsure makes that many EnsurePR calls fail before any succeeds,
	// without creating or touching the PR: it models a request that never
	// reached the provider at all.
	FailEnsure int
	// FailEnsureAfterCreate makes that many EnsurePR calls create or
	// update the PR as usual and still report failure: it models a
	// request the provider actually applied, whose success response was
	// then lost (a dropped connection, a 5xx after the write landed). The
	// PR created or found this way must survive in State.
	FailEnsureAfterCreate int
	// PartialEnsure, when set, makes EnsurePR create or update the PR
	// with its Draft state forced to PartialEnsureDraft — regardless of
	// spec.Draft — and report a *gitprov.PartialError wrapping this
	// error, simulating a provider that could only partly apply the
	// requested PR settings.
	PartialEnsure      error
	PartialEnsureDraft bool
	mu                 sync.Mutex
	State              State
}

// Load reads a persisted state file; a missing file is an empty state.
func Load(path string) (State, error) {
	var st State
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	return st, json.Unmarshal(data, &st)
}

func (p *Provider) load() error {
	if p.Path == "" {
		return nil
	}
	st, err := Load(p.Path)
	p.State = st
	return err
}

func (p *Provider) save() error {
	if p.Path == "" {
		return nil
	}
	data, err := json.MarshalIndent(p.State, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p.Path, data, 0o644)
}

// EnsurePR implements gitprov.Provider.
func (p *Provider) EnsurePR(_ context.Context, spec gitprov.PRSpec) (gitprov.PR, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.load(); err != nil {
		return gitprov.PR{}, err
	}
	if p.FailEnsureAfterCreate > 0 {
		p.FailEnsureAfterCreate--
		pr, err := p.ensureLocked(spec.Branch, spec.Draft, spec)
		if err != nil {
			return gitprov.PR{}, err
		}
		return pr, errors.New("fake provider: injected EnsurePR failure after creating the PR")
	}
	if p.FailEnsure > 0 {
		p.FailEnsure--
		return gitprov.PR{}, errors.New("fake provider: injected EnsurePR failure")
	}
	if p.PartialEnsure != nil {
		pr, err := p.ensureLocked(spec.Branch, p.PartialEnsureDraft, spec)
		if err != nil {
			return gitprov.PR{}, err
		}
		return pr, &gitprov.PartialError{Err: p.PartialEnsure}
	}
	return p.ensureLocked(spec.Branch, spec.Draft, spec)
}

// ensureLocked finds the PR for branch and updates its draft state, or
// creates it, saving the state either way. Callers hold p.mu and have
// already called p.load.
func (p *Provider) ensureLocked(branch string, draft bool, spec gitprov.PRSpec) (gitprov.PR, error) {
	for i := range p.State.PRs {
		if p.State.PRs[i].Spec.Branch == branch {
			p.State.PRs[i].Draft = draft
			return p.State.PRs[i].PR, p.save()
		}
	}
	n := len(p.State.PRs) + 1
	pr := gitprov.PR{Number: n, URL: fmt.Sprintf("https://example.invalid/pr/%d", n), Draft: draft}
	p.State.PRs = append(p.State.PRs, PRState{PR: pr, Spec: spec})
	return pr, p.save()
}

// Comment implements gitprov.Provider.
func (p *Provider) Comment(_ context.Context, pr gitprov.PR, body string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.load(); err != nil {
		return err
	}
	for i := range p.State.PRs {
		if p.State.PRs[i].Number == pr.Number {
			p.State.PRs[i].Comments = append(p.State.PRs[i].Comments, body)
			return p.save()
		}
	}
	return fmt.Errorf("fake provider: no PR #%d", pr.Number)
}

// GitAuth implements gitprov.Provider.
func (p *Provider) GitAuth(_ context.Context, minValid time.Duration) (gitprov.GitAuth, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Auth == nil {
		return gitprov.GitAuth{}, nil
	}
	return p.Auth(minValid), nil
}
