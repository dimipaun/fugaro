package localcfg

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
)

// Checkout is the checkout a command acts on, when its git toplevel holds
// a fugaro.yaml.
type Checkout struct {
	Root    string // git toplevel
	Project string // its fugaro.yaml's project:, "" when absent
}

// SelectInput is everything that can select a project config.
type SelectInput struct {
	Config     string    // --config
	Project    string    // --project
	Checkout   *Checkout // nil outside a checkout (no git toplevel, or no fugaro.yaml there)
	EnvProject string    // $FUGARO_PROJECT
	EnvConfig  string    // $FUGARO_CONFIG
	// Creating is fugaro init, which writes the project config: a missing
	// one is not refused (the selection then has no config), but the
	// checkout's project still has to agree.
	Creating bool
	Getenv   func(string) string
}

// Selection is the project config a command acts on, and why.
type Selection struct {
	// Path is the project config's file; Name its project. With Creating
	// and no config, Path is where it goes when a name is known, and both
	// may be "".
	Path, Name string
	// From is what selected it: "--config", "--project", "checkout",
	// "FUGARO_PROJECT", "FUGARO_CONFIG" or "only project config".
	From string
	// Notes are selectors that lost to a higher one, for stderr.
	Notes []string
}

// SelectError is a refusal to pick a project config (exit 1).
type SelectError struct {
	Msg string
	// Projects are the project configs there are, when the refusal lists
	// them.
	Projects []string
}

func (e *SelectError) Error() string { return e.Msg }

// Select picks the project config a command acts on. Explicit selectors
// never lose to the environment; first match wins:
//
//  1. --config <file>;
//  2. --project <name>;
//  3. the checkout's fugaro.yaml project:;
//  4. $FUGARO_PROJECT;
//  5. $FUGARO_CONFIG;
//  6. exactly one project config.
//
// Two selectors naming different projects are refused (the checkout
// against --config, --project or $FUGARO_PROJECT; --config against
// --project), never resolved silently. $FUGARO_CONFIG and $FUGARO_PROJECT
// are ignored with a note when something above them selects another
// project; $FUGARO_CONFIG naming the selected project is that project's
// config, and its file is used.
func Select(in SelectInput) (Selection, *Config, error) {
	s := selector{in: in}
	return s.run()
}

type selector struct {
	in    SelectInput
	names []string
	read  bool
}

func (s *selector) projects() []string {
	if !s.read {
		s.names, _ = Projects(s.in.Getenv)
		s.read = true
	}
	return s.names
}

func (s *selector) refuse(list bool, format string, a ...any) error {
	e := &SelectError{Msg: fmt.Sprintf(format, a...)}
	if list {
		e.Projects = s.projects()
	}
	return e
}

// where names a selector in a note or a refusal.
func where(from string) string {
	if from == "checkout" {
		return "this checkout"
	}
	return from
}

func (s *selector) run() (Selection, *Config, error) {
	in := s.in
	co := in.Checkout
	if co != nil && co.Project == "" {
		return Selection{}, nil, s.refuse(false, "this checkout's fugaro.yaml has no `project:`; add `project: <name>` (fugaro config example shows it)")
	}
	// 1. --config.
	if in.Config != "" {
		sel, c, err := s.file(in.Config, "--config")
		if err != nil || c == nil {
			return sel, c, err
		}
		switch {
		case in.Project != "" && in.Project != c.Name:
			return Selection{}, nil, s.refuse(false, "--config names project %s, but --project says %s", c.Name, in.Project)
		case co != nil && co.Project != c.Name:
			return Selection{}, nil, s.refuse(false, "this checkout belongs to project %s; --config names project %s", co.Project, c.Name)
		}
		return s.withNotes(sel, c, false)
	}
	// 2. --project.
	if in.Project != "" {
		if co != nil && co.Project != in.Project {
			return Selection{}, nil, s.refuse(false, "this checkout belongs to project %s; --project says %s", co.Project, in.Project)
		}
		return s.named(in.Project, "--project")
	}
	// 3. The checkout.
	if co != nil {
		if in.EnvProject != "" && in.EnvProject != co.Project {
			return Selection{}, nil, s.refuse(false, "this checkout belongs to project %s; FUGARO_PROJECT says %s", co.Project, in.EnvProject)
		}
		return s.named(co.Project, "checkout")
	}
	// 4. $FUGARO_PROJECT.
	if in.EnvProject != "" {
		return s.named(in.EnvProject, "FUGARO_PROJECT")
	}
	// 5. $FUGARO_CONFIG.
	if in.EnvConfig != "" {
		return s.file(in.EnvConfig, "FUGARO_CONFIG")
	}
	// 6. The only project config.
	switch names := s.projects(); len(names) {
	case 1:
		c, path, err := LoadProject(in.Getenv, names[0])
		if err != nil {
			return Selection{}, nil, err
		}
		return Selection{Path: path, Name: c.Name, From: "only project config"}, c, nil
	case 0:
		if in.Creating {
			return Selection{}, nil, nil
		}
		msg := "no project config; see `fugaro init --config-only`"
		if p := legacyPath(in.Getenv); p != "" {
			if _, err := os.Stat(p); err == nil {
				msg += fmt.Sprintf(" (the old %s isn't read any more: see docs/design/m9-budget-and-dashboard.md §13.1)", p)
			}
		}
		return Selection{}, nil, s.refuse(false, "%s", msg)
	default:
		return Selection{}, nil, s.refuse(true, "several project configs (%s) and nothing selects one: pass --project <name>, set FUGARO_PROJECT, or run from a checkout whose fugaro.yaml names its project",
			strings.Join(names, ", "))
	}
}

// file loads an explicit config file, which --config or $FUGARO_CONFIG
// named. A missing one is fugaro init's to create.
func (s *selector) file(path, from string) (Selection, *Config, error) {
	c, err := Load(path)
	if errors.Is(err, ErrMissing) && s.in.Creating {
		return Selection{Path: path, From: from}, nil, nil
	}
	if err != nil {
		return Selection{}, nil, fmt.Errorf("%s: %w", from, err)
	}
	return Selection{Path: path, Name: c.Name, From: from}, c, nil
}

// named selects project name's config, which from named.
func (s *selector) named(name, from string) (Selection, *Config, error) {
	in := s.in
	// $FUGARO_CONFIG naming this very project is its config.
	if in.EnvConfig != "" {
		if c, err := Load(in.EnvConfig); err == nil && c.Name == name {
			return s.withNotes(Selection{Path: in.EnvConfig, Name: name, From: from}, c, true)
		}
	}
	if !config.ProjectNameRE.MatchString(name) && from == "checkout" {
		return Selection{}, nil, s.refuse(false, "this checkout's fugaro.yaml names project %q, which is not a project name (1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit)", name)
	}
	if !config.ProjectNameRE.MatchString(name) {
		return Selection{}, nil, s.refuse(true, "%s %q is not a project name; the projects are: %s (the GCP project is --gcp-project)",
			where(from), name, s.list())
	}
	c, path, err := LoadProject(in.Getenv, name)
	switch {
	case errors.Is(err, ErrMissing) && in.Creating:
		return s.withNotes(Selection{Path: path, Name: name, From: from}, nil, false)
	case errors.Is(err, ErrMissing) && from == "checkout":
		return Selection{}, nil, s.refuse(false, "this checkout belongs to project %s; there is no project config for %s (run `fugaro init --config-only --gcp-project <id>`)", name, name)
	case errors.Is(err, ErrMissing):
		return Selection{}, nil, s.refuse(true, "%s: there is no project config for %s; the projects are: %s (the GCP project is --gcp-project)",
			where(from), name, s.list())
	case err != nil:
		return Selection{}, nil, err
	}
	return s.withNotes(Selection{Path: path, Name: name, From: from}, c, false)
}

func (s *selector) list() string {
	if names := s.projects(); len(names) > 0 {
		return strings.Join(names, ", ")
	}
	return "none yet"
}

// withNotes adds a note for each ambient selector that names another
// project than sel, which it lost to. usedEnvConfig: $FUGARO_CONFIG is the
// selected file.
func (s *selector) withNotes(sel Selection, c *Config, usedEnvConfig bool) (Selection, *Config, error) {
	in := s.in
	if in.EnvProject != "" && in.EnvProject != sel.Name && sel.From != "FUGARO_PROJECT" {
		sel.Notes = append(sel.Notes, fmt.Sprintf("ignoring FUGARO_PROJECT (%s): %s selects %s", in.EnvProject, where(sel.From), sel.Name))
	}
	if in.EnvConfig != "" && !usedEnvConfig && sel.From != "FUGARO_CONFIG" && in.EnvConfig != sel.Path {
		what := "unreadable"
		if ec, err := Load(in.EnvConfig); err == nil {
			what = "project " + ec.Name
		}
		sel.Notes = append(sel.Notes, fmt.Sprintf("ignoring FUGARO_CONFIG (%s): %s selects %s", what, where(sel.From), sel.Name))
	}
	return sel, c, nil
}
