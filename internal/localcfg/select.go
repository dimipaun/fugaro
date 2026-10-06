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
	// GCPProject is its fugaro.yaml's gcp_project:, "" when absent.
	GCPProject string
}

// SelectInput is everything that can select a project config.
type SelectInput struct {
	Config     string    // --config
	Project    string    // --project
	Checkout   *Checkout // nil outside a checkout (no git toplevel, or no fugaro.yaml there)
	EnvProject string    // $FUGARO_PROJECT
	EnvConfig  string    // $FUGARO_CONFIG
	// Name is fugaro init's --name: where nothing else selects one of
	// several project configs it is the project's name, which has a config or
	// is to be created. Only with Creating.
	Name string
	// Origin, when set, gives the checkout's origin host and repository (owner/name):
	// where nothing else selects one of several project configs, the one
	// project that lists it is selected. Called only then.
	Origin func() (host, repo string)
	// Creating is fugaro init, which writes the project config: a missing
	// one is not refused (the selection then has no config), but the
	// checkout's project still has to agree.
	Creating bool
	Getenv   func(string) string
	// GCPProject is the GCP project the checkout's fugaro.yaml names, or
	// --gcp-project: the only way a shared config is found. Never guessed.
	GCPProject string
	// Shared, when set (cloud commands), fetches project name's published
	// config from the runs bucket of gcpProject, with an optional note. It is
	// tried only where no local config exists. Nil is the offline commands.
	Shared func(name, gcpProject string) (*Config, string, error)
}

// Selection is the project config a command acts on, and why.
type Selection struct {
	// Path is the project config's file; Name its project. With Creating
	// and no config, Path is where it goes when a name is known, and both
	// may be "".
	Path, Name string
	// From is what selected it: "--config", "--project", "checkout",
	// "FUGARO_PROJECT", "FUGARO_CONFIG", "only project config" or "shared
	// config" (the one published to the runs bucket: Path is "" then, and
	// nothing may write it).
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
//  6. exactly one project config (unless fugaro init's --name names another
//     project: that project's first run).
//
// Two selectors naming different projects are refused (the checkout
// against --config, --project or $FUGARO_PROJECT; --config against
// --project), never resolved silently. $FUGARO_PROJECT is ignored with a
// note when --config or --project, outside a checkout, selects another
// project; $FUGARO_CONFIG likewise when anything above it does.
// $FUGARO_CONFIG naming the project a higher step selected is used only
// when that project has no config under projects/; when it has one, that
// one is used (with a note), and a $FUGARO_CONFIG disagreeing with it on
// the GCP project or the runs bucket is refused.
func Select(in SelectInput) (Selection, *Config, error) {
	s := selector{in: in}
	return s.run()
}

type selector struct {
	in    SelectInput
	names []string
	err   error
	read  bool
}

// projects are the project configs there are; an error reading their
// directory is returned, never taken for "none".
func (s *selector) projects() ([]string, error) {
	if !s.read {
		s.names, s.err = Projects(s.in.Getenv)
		if s.err != nil {
			s.err = fmt.Errorf("listing the project configs: %w", s.err)
		}
		s.read = true
	}
	return s.names, s.err
}

func (s *selector) refuse(format string, a ...any) error {
	return &SelectError{Msg: fmt.Sprintf(format, a...)}
}

// refuseListing is refuse, listing the project configs at the end of the
// message, and in the error.
func (s *selector) refuseListing(format string, a ...any) error {
	names, err := s.projects()
	if err != nil {
		return err
	}
	list := "none yet"
	if len(names) > 0 {
		list = strings.Join(names, ", ")
	}
	return &SelectError{Msg: fmt.Sprintf(format, a...) + "; the projects are: " + list + " (the GCP project is --gcp-project)", Projects: names}
}

// where names a selector in a note or a refusal.
func where(from string) string {
	if from == "checkout" {
		return "this checkout"
	}
	return from
}

// nameRule is how a project name is spelled, for refusals.
const nameRule = "1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit"

func (s *selector) run() (Selection, *Config, error) {
	in := s.in
	co := in.Checkout
	// The checkout's own rules, whatever selects the project: it must name
	// a project, and the environment can't say another.
	if co != nil {
		switch {
		case co.Project == "":
			return Selection{}, nil, s.refuse("this checkout's fugaro.yaml has no `project:`; add `project: <name>` (fugaro config example shows it)")
		case !config.ProjectNameRE.MatchString(co.Project):
			return Selection{}, nil, s.refuse("this checkout's fugaro.yaml names project %q, which is not a project name (%s)", co.Project, nameRule)
		case in.EnvProject != "" && in.EnvProject != co.Project:
			return Selection{}, nil, s.refuse("this checkout belongs to project %s; FUGARO_PROJECT says %s", co.Project, in.EnvProject)
		case in.Project != "" && in.Project != co.Project:
			return Selection{}, nil, s.refuse("this checkout belongs to project %s; --project says %s", co.Project, in.Project)
		}
	}
	// 1. --config.
	if in.Config != "" {
		c, err := Load(in.Config)
		if errors.Is(err, ErrMissing) && in.Creating {
			// fugaro init creates it, for the project the other
			// selectors name, which the checkout already agreed with.
			name := in.Project
			if co != nil {
				name = co.Project
			}
			return s.withNotes(Selection{Path: in.Config, Name: name, From: "--config"}, nil)
		}
		if err != nil {
			return Selection{}, nil, fmt.Errorf("--config: %w", err)
		}
		switch {
		case in.Project != "" && in.Project != c.Name:
			return Selection{}, nil, s.refuse("--config names project %s, but --project says %s", c.Name, in.Project)
		case co != nil && co.Project != c.Name:
			return Selection{}, nil, s.refuse("this checkout belongs to project %s; --config names project %s", co.Project, c.Name)
		}
		return s.withNotes(Selection{Path: in.Config, Name: c.Name, From: "--config"}, c)
	}
	// 2. --project.
	if in.Project != "" {
		return s.named(in.Project, "--project")
	}
	// 3. The checkout.
	if co != nil {
		return s.named(co.Project, "checkout")
	}
	// 4. $FUGARO_PROJECT.
	if in.EnvProject != "" {
		return s.named(in.EnvProject, "FUGARO_PROJECT")
	}
	// 5. $FUGARO_CONFIG.
	if in.EnvConfig != "" {
		c, err := Load(in.EnvConfig)
		if errors.Is(err, ErrMissing) && in.Creating {
			return Selection{Path: in.EnvConfig, From: "FUGARO_CONFIG"}, nil, nil
		}
		if err != nil {
			return Selection{}, nil, fmt.Errorf("FUGARO_CONFIG: %w", err)
		}
		return Selection{Path: in.EnvConfig, Name: c.Name, From: "FUGARO_CONFIG"}, c, nil
	}
	// 6. The only project config.
	names, err := s.projects()
	if err != nil {
		return Selection{}, nil, err
	}
	switch len(names) {
	case 1:
		// --name (fugaro init) for another project than the only one is a
		// first run of that project, as with several configs. Renaming the
		// installation is refused later, by the installation's own name.
		if in.Creating && in.Name != "" && in.Name != names[0] {
			return s.named(in.Name, "--name")
		}
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
		return Selection{}, nil, s.refuse("%s", msg)
	default:
		// --name (fugaro init) names the project, for a first run too.
		if in.Creating && in.Name != "" {
			return s.named(in.Name, "--name")
		}
		note := ""
		if in.Origin != nil {
			if host, repo := in.Origin(); repo != "" && host != "" {
				sel, c, listed := s.byOrigin(names, host, repo)
				if c != nil {
					return sel, c, nil
				}
				if len(listed) > 1 {
					note = fmt.Sprintf(" (the checkout's origin repository %s is listed by %s)", strings.ToLower(repo), strings.Join(listed, ", "))
				}
			}
		}
		return Selection{}, nil, &SelectError{Projects: names, Msg: fmt.Sprintf(
			"several project configs (%s) and nothing selects one%s: run `export FUGARO_PROJECT=<name>` (or pass --project <name>), or run from a checkout whose fugaro.yaml names its project",
			strings.Join(names, ", "), note)}
	}
}

// named selects project name's config, which from named: its
// projects/<name>.yaml, else a $FUGARO_CONFIG holding that project.
func (s *selector) named(name, from string) (Selection, *Config, error) {
	in := s.in
	if !config.ProjectNameRE.MatchString(name) {
		return Selection{}, nil, s.refuseListing("%s %q is not a project name (%s)", from, name, nameRule)
	}
	c, path, err := LoadProject(in.Getenv, name)
	if errors.Is(err, ErrMissing) && in.EnvConfig != "" {
		// No canonical config: a $FUGARO_CONFIG holding this very project
		// is its config.
		if ec, eerr := Load(in.EnvConfig); eerr == nil && ec.Name == name {
			return s.withNotes(Selection{Path: in.EnvConfig, Name: name, From: from}, ec)
		}
	}
	switch {
	case errors.Is(err, ErrMissing) && in.Creating:
		return s.withNotes(Selection{Path: path, Name: name, From: from}, nil)
	case errors.Is(err, ErrMissing) && from == "checkout":
		if sel, c, ok, serr := s.shared(name, from); ok {
			return sel, c, serr
		}
		if in.Shared != nil {
			return Selection{}, nil, s.refuse("this checkout belongs to project %s; there is no project config for %s: add `gcp_project: <id>` next to `project:` in fugaro.yaml (whoever onboarded the repository can run fugaro init --repo to add it), or run `fugaro init --config-only --gcp-project <id>`", name, name)
		}
		return Selection{}, nil, s.refuse("this checkout belongs to project %s; there is no project config for %s (run `fugaro init --config-only --gcp-project <id>`)", name, name)
	case errors.Is(err, ErrMissing):
		if sel, c, ok, serr := s.shared(name, from); ok {
			return sel, c, serr
		}
		return Selection{}, nil, s.refuseListing("%s names project %s, which has no project config", from, name)
	case err != nil:
		return Selection{}, nil, err
	}
	return s.withNotes(Selection{Path: path, Name: name, From: from}, c)
}

// shared fetches the project's published config when the caller allows it
// (cloud commands) and the GCP project is known from the checkout or
// --gcp-project; ok says it did (or tried to: err). The GCP project is never
// guessed.
func (s *selector) shared(name, from string) (sel Selection, c *Config, ok bool, err error) {
	in := s.in
	if in.Shared == nil || in.GCPProject == "" || in.Creating {
		return Selection{}, nil, false, nil
	}
	c, note, err := in.Shared(name, in.GCPProject)
	if err != nil {
		return Selection{}, nil, true, err
	}
	sel = Selection{Name: name, From: "shared config"}
	if note != "" {
		sel.Notes = append(sel.Notes, note)
	}
	return sel, c, true, nil
}

// withNotes adds a note for each ambient selector that lost to sel's, or
// refuses a $FUGARO_CONFIG that is another copy of sel's project config
// disagreeing with it.
func (s *selector) withNotes(sel Selection, c *Config) (Selection, *Config, error) {
	in := s.in
	if in.EnvProject != "" && in.EnvProject != sel.Name && sel.From != "FUGARO_PROJECT" {
		sel.Notes = append(sel.Notes, fmt.Sprintf("ignoring FUGARO_PROJECT (%s): %s selects %s", in.EnvProject, where(sel.From), sel.Name))
	}
	if in.EnvConfig == "" || in.EnvConfig == sel.Path {
		return sel, c, nil
	}
	ec, err := Load(in.EnvConfig)
	switch {
	case err != nil:
		sel.Notes = append(sel.Notes, fmt.Sprintf("ignoring FUGARO_CONFIG (unreadable: %v): %s selects %s", err, where(sel.From), sel.Name))
	case ec.Name != sel.Name:
		sel.Notes = append(sel.Notes, fmt.Sprintf("ignoring FUGARO_CONFIG (project %s): %s selects %s", ec.Name, where(sel.From), sel.Name))
	case c != nil && (ec.GCPProject != c.GCPProject || ec.RunsBucketName() != c.RunsBucketName() || ec.BucketURL() != c.BucketURL()):
		return Selection{}, nil, s.refuse("FUGARO_CONFIG %s and %s are both project %s's config, but disagree on its GCP project or runs bucket (%s, %s against %s, %s); remove or fix one",
			in.EnvConfig, sel.Path, sel.Name, ec.GCPProject, ec.BucketURL(), c.GCPProject, c.BucketURL())
	default:
		sel.Notes = append(sel.Notes, fmt.Sprintf("ignoring FUGARO_CONFIG (project %s): project %s's config is %s", ec.Name, sel.Name, sel.Path))
	}
	return sel, c, nil
}

// ProviderHosts is the host each provider's repositories live on.
var ProviderHosts = map[string]string{"github": "github.com", "bitbucket": "bitbucket.org"}

// byOrigin selects the project config that lists repo among its repos, when
// exactly one does; otherwise it returns no config (nothing is guessed), and
// listed names the projects that do list it.
func (s *selector) byOrigin(names []string, host, repo string) (sel Selection, c *Config, listed []string) {
	var hitCfg *Config
	var hitPath string
	for _, n := range names {
		pc, path, err := LoadProject(s.in.Getenv, n)
		if err != nil {
			continue // an unreadable config is not a candidate; selecting it would fail on its own
		}
		for r, rc := range pc.Repos {
			// The same repository name on another host is another repository.
			if ProviderHosts[rc.Provider] != strings.ToLower(host) {
				continue
			}
			if strings.EqualFold(strings.TrimSuffix(r, ".git"), strings.TrimSuffix(repo, ".git")) {
				listed = append(listed, n)
				hitCfg, hitPath = pc, path
				break
			}
		}
	}
	if len(listed) != 1 {
		return Selection{}, nil, listed
	}
	return Selection{Path: hitPath, Name: listed[0], From: "the checkout's origin repository " + strings.ToLower(repo)}, hitCfg, listed
}
