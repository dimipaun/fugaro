// Package workspace is the workspace record: what a workspace states about
// the group -- the members and the cap on what the workspace's own credentials
// pay for -- and nothing else (docs/workspaces/spec.md §2).
//
// The record is declared locally by the workspace owner and published by
// `fugaro update` at `workspaces/<name>` in the workspace's own database
// (D1292); its readers are the workspace's own service and the workspace
// owner's CLI, and no member reads it -- a member's machine holds no grant on
// the workspace's project at all. What the record does not carry is as
// load-bearing as what it does: no identity provider, no issuer, no
// assertion-signing key and no service account. Every one of those is the
// member's own pin, in the member's own config (W1), because a record that
// could supply one would be a record that could aim every member's exchange
// and every member's database grant at a party of its choosing -- the one
// direction rule 1 of the spec does not survive. Parse decodes strictly, so
// the key for one of them is refused at the decode rather than read and
// ignored.
//
// Money is integer micro-dollars (pricing.Micros) from Parse onward: the file
// writes a dollar amount, Parse converts it once with pricing.FromUSD, and
// nothing below that point does money arithmetic in a float.
package workspace

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// Record is one workspace's record.
type Record struct {
	// Name is the workspace's name. Validate holds it to the project-name
	// grammar (config.ProjectNameRE) rather than a second one (F21): a
	// workspace name and a project name are written and read by the same
	// flags, and two grammars would be two things two writers could disagree
	// about.
	Name string
	// Members are the projects in the workspace, in project-name order and
	// each once. A project may be a member of at most one workspace (D1291),
	// which is why the member's own config carries the membership and not
	// this list.
	Members []Member
	// Cap is the workspace's own spend ceiling. It bounds what the
	// workspace's own credentials pay for and nothing else: it is not a
	// member's cap, and a member's own cap is never widened by it (D1300).
	Cap Cap
}

// Member is one project in the workspace: its Fugaro project name, which is
// the name its own config carries, and the repositories the workspace groups.
type Member struct {
	Project string
	Repos   []string
}

// Cap is the workspace's spend ceiling. Set is false for a record that states
// none, which is not a cap of zero: a cap of zero would forbid every call the
// workspace's credentials pay for, and the state W2's publish stage refuses is
// a workspace with no members rather than one with no cap. Monthly is integer
// micro-dollars, converted once from the dollar amount the file writes.
type Cap struct {
	Monthly pricing.Micros
	Set     bool
}

// fileRecord is the record as the file writes it: the shape
// schemas/workspace.schema.json describes. It is a type of its own because the
// file writes money as dollars and Record holds micro-dollars, and the
// conversion is Parse's one job; every other field is carried through
// unchanged.
type fileRecord struct {
	Name    string       `yaml:"name"`
	Members []fileMember `yaml:"members"`
	Cap     *fileCap     `yaml:"cap,omitempty"`
}

type fileMember struct {
	Project string   `yaml:"project"`
	Repos   []string `yaml:"repos"`
}

// fileCap is a cap that is written: a pointer, so a record that states no cap
// is the key left out or written as null (the convention every optional block
// in this program follows), while a cap block that is there must carry a
// positive amount.
type fileCap struct {
	Monthly float64 `yaml:"monthly"`
}

// Parse reads a workspace record. It decodes strictly -- an unknown field is an
// error, as every config and wire format here does -- converts the cap from
// dollars to micro-dollars once, and holds the result to Validate's rules. A
// file it returns is one this package reads back unchanged.
func Parse(raw []byte) (*Record, error) {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var f fileRecord
	if err := dec.Decode(&f); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("the file is empty")
		}
		return nil, err
	}
	r := &Record{Name: f.Name, Members: make([]Member, 0, len(f.Members))}
	for _, m := range f.Members {
		r.Members = append(r.Members, Member{Project: m.Project, Repos: m.Repos})
	}
	if f.Cap != nil {
		monthly, err := pricing.FromUSD(f.Cap.Monthly)
		if err != nil {
			return nil, fmt.Errorf("cap.monthly: %v", err)
		}
		r.Cap = Cap{Monthly: monthly, Set: true}
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return r, nil
}

// Validate checks the rules the record's shape does not carry: the names, the
// two lists' order, and the cap. It is what a caller that built a record
// itself calls before writing one -- the CLI's `workspace cap set` is the
// first (W8) -- and Parse calls it, so a file and a built record are held to
// one rule.
func (r *Record) Validate() error {
	var errs []error
	if !config.ProjectNameRE.MatchString(r.Name) {
		errs = append(errs, fmt.Errorf("name: %q is not a project name", r.Name))
	}
	for i, m := range r.Members {
		if !config.ProjectNameRE.MatchString(m.Project) {
			errs = append(errs, fmt.Errorf("members[%d].project: %q is not a project name", i, m.Project))
		}
		if i > 0 {
			switch prev := r.Members[i-1].Project; {
			case m.Project == prev:
				errs = append(errs, fmt.Errorf("members[%d]: %s is listed twice", i, m.Project))
			case m.Project < prev:
				errs = append(errs, fmt.Errorf("members[%d]: %s is out of order after %s; the list is sorted by project", i, m.Project, prev))
			}
		}
		// A member's repositories are a set written as a list for the same
		// reason the member list is: the published node is compared against
		// these strings, and two spellings of one set are two records that
		// say the same thing and compare unequal.
		for j, repo := range m.Repos {
			if j == 0 {
				continue
			}
			switch prev := m.Repos[j-1]; {
			case repo == prev:
				errs = append(errs, fmt.Errorf("members[%d].repos[%d]: %s is listed twice", i, j, repo))
			case repo < prev:
				errs = append(errs, fmt.Errorf("members[%d].repos[%d]: %s is out of order after %s; the list is sorted", i, j, repo, prev))
			}
		}
	}
	switch {
	case r.Cap.Set && r.Cap.Monthly <= 0:
		errs = append(errs, errors.New("cap.monthly: a cap that is set must be more than zero; leave the cap block out to state none"))
	case !r.Cap.Set && r.Cap.Monthly != 0:
		errs = append(errs, errors.New("cap.monthly: an amount nothing reads; the record states no cap, so it carries no amount"))
	}
	return errors.Join(errs...)
}
