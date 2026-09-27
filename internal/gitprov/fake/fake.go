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
	Path  string // optional; when set, State is loaded and saved on every call
	mu    sync.Mutex
	State State
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
	for i := range p.State.PRs {
		if p.State.PRs[i].Spec.Branch == spec.Branch {
			p.State.PRs[i].Draft = spec.Draft
			return p.State.PRs[i].PR, p.save()
		}
	}
	n := len(p.State.PRs) + 1
	pr := gitprov.PR{Number: n, URL: fmt.Sprintf("https://example.invalid/pr/%d", n), Draft: spec.Draft}
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
