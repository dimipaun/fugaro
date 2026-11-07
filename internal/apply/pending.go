package apply

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// The pending document: the local record of the steps this machine still owes
// for the changes made on it (docs/composite-commands, D1562, D1567, D1570,
// D1575).
//
// It exists because a management command may be told to save a change without
// running its steps (`--no-apply`), and because a step may fail after the
// change is saved: in both cases the change is in the config and the thing that
// makes it live has not run, and nothing else on this machine remembers that.
// `fugaro apply` reads this file and runs what it names; `fugaro update` clears
// the entries of the stages it ran successfully and leaves the rest, the
// catalog build among them (D1567).
//
// It is a hint and never authority (D1575). It sits beside the project config
// it belongs to, every entry is bound to the project name and the GCP project
// ID it was queued for, and the reader refuses an entry bound to another — a
// file copied to another machine or another project is a mistake, not a plan.
// Each step checks the state itself before it does anything, so an entry that
// is stale, duplicated or wrong costs a read and no change. The one thing the
// file may never hold is a value: it names the change that asked for the step
// (a setting key or a resource type), never what was set, so it can never
// become a second copy of the config or a place a secret is written down
// (D1570).

// PendingVersion is the version of the pending document. A file whose version
// is not this one is refused rather than read as this version's, and is never
// written over: a newer fugaro's record of what is owed survives an older one.
const PendingVersion = 1

// PendingFileSuffix is the pending document's name beside the project config:
// projects/<name>.pending.json. The dot is the whole of why it can sit there —
// a project name is a-z, 0-9 and '-' (config.ProjectNameRE), so a file named
// this way can never be read back as a project config by localcfg.Projects
// (localcfg.LayerFile's rule, for its reason).
const PendingFileSuffix = ".pending.json"

// maxPendingBytes bounds the read: an entry is a step name and a change, and a
// project owes a handful of steps at a time, so anything near this is not one.
const maxPendingBytes = 1 << 20

// ErrProjectMismatch is what the reader returns for an entry bound to another
// project or another GCP project ID (D1575). The error names both sides.
var ErrProjectMismatch = errors.New("the pending steps are bound to another project")

// errPendingVersion marks the one refusal SavePending must not write over: a
// document a newer fugaro wrote.
var errPendingVersion = errors.New("the pending steps are from another version of fugaro")

// PendingStep is one step this machine owes, and the change that asked for it.
type PendingStep struct {
	// Step is the step's name: an initflow stage, or CatalogBuild.
	Step string `json:"step"`
	// Project is the Fugaro project name the entry was queued for, and
	// GCPProject the GCP project ID of the installation it was queued in. They
	// are what makes a file that travelled — copied to another machine, or
	// restored into another project's config directory — refuse rather than
	// run: the steps it names would be run against an installation the change
	// was never made for (D1575).
	Project    string `json:"project"`
	GCPProject string `json:"gcp_project"`
	// Change is what asked for the step. It is shown to the person running
	// `fugaro apply` so the step is not a mystery, and it is the change's
	// subject only: never a value.
	Change Change `json:"change"`
}

// Pending is the whole document.
type Pending struct {
	Version int `json:"version"`
	// LastApply is the stamp `doctor` compares: the caller's hash of the
	// project config's bytes at the last apply that finished, empty when none
	// has. It is what tells a machine that owes nothing from one whose file was
	// deleted or lost: a stamp that no longer matches the config, with no
	// pending file, says the config changed since and nothing owed is recorded
	// (D1575). This package neither writes nor compares it — the caller that
	// has the config in hand does both.
	LastApply string `json:"last_apply,omitempty"`
	// Steps is what is owed, and it is written even when it is empty: `[]` is
	// "this machine owes nothing", which is a different answer from a document
	// still carrying an earlier change's steps.
	Steps []PendingStep `json:"steps"`
}

// PendingPath is where a project's pending document lives: beside its project
// config, under the config home's projects/ directory.
func PendingPath(getenv func(string) string, project string) (string, error) {
	if !config.ProjectNameRE.MatchString(project) {
		return "", fmt.Errorf("%q is not a project name", project)
	}
	dir, err := localcfg.ProjectsDir(getenv)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, project+PendingFileSuffix), nil
}

// EmptyPending is the document of a machine that owes nothing: what a reader
// gets for a file that was never written or was deleted, which is not an error
// (D1575: a missing file records nothing as owed).
func EmptyPending() *Pending {
	return &Pending{Version: PendingVersion, Steps: []PendingStep{}}
}

// LoadPending reads project's pending document and checks every entry against
// the project name and GCP project ID the caller is asking for. A machine with
// no document is an empty one and no error; a file that cannot be read, is
// larger than the bound, is not a regular file, is not JSON, carries a member
// this build does not know (the format decodes strictly, CLAUDE.md), is not
// this version, or holds an entry bound elsewhere, is an error — a reader that
// took a file it could not read for an empty one would then write over it.
func LoadPending(getenv func(string) string, project, gcpProject string) (*Pending, error) {
	path, err := PendingPath(getenv, project)
	if err != nil {
		return nil, err
	}
	data, err := config.ReadRegular(path, maxPendingBytes)
	if errors.Is(err, os.ErrNotExist) {
		return EmptyPending(), nil
	}
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var p Pending
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("%s is not a pending-steps document: %w", path, err)
	}
	if p.Version != PendingVersion {
		return nil, fmt.Errorf("%s is version %d of the pending steps, and this fugaro writes version %d: %w",
			path, p.Version, PendingVersion, errPendingVersion)
	}
	for _, s := range p.Steps {
		if s.Project != project || s.GCPProject != gcpProject {
			return nil, fmt.Errorf("%s: the step %q is bound to project %q in GCP project %q, and this is project %q in %q: %w",
				path, s.Step, s.Project, s.GCPProject, project, gcpProject, ErrProjectMismatch)
		}
	}
	if p.Steps == nil {
		p.Steps = []PendingStep{}
	}
	return &p, nil
}

// SavePending writes project's pending document whole: beside the project
// config, 0600 in a directory created 0700, through a rename, so a reader never
// sees half a document.
//
// A document a newer fugaro wrote is refused and left alone — this build cannot
// read it, and rebuilding it from nothing would drop that fugaro's record of
// what is owed. A document this build cannot parse is state gone wrong and is
// written over rather than left refusing every later write: the file is a hint,
// and what says whether anything was owed is the last_apply stamp `doctor`
// compares (D1575).
func SavePending(getenv func(string) string, project string, p *Pending) error {
	path, err := PendingPath(getenv, project)
	if err != nil {
		return err
	}
	if v, err := pendingVersion(path); err == nil && v != 0 && v != PendingVersion {
		return fmt.Errorf("%s is version %d of the pending steps, and this fugaro writes version %d: %w",
			path, v, PendingVersion, errPendingVersion)
	}
	p.Version = PendingVersion
	if p.Steps == nil {
		p.Steps = []PendingStep{}
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return writePendingFile(path, append(data, '\n'))
}

// pendingVersion is the version the document at path carries: 0 for a machine
// that has none, and the read or parse error for a file this build cannot read
// as one.
func pendingVersion(path string) (int, error) {
	data, err := config.ReadRegular(path, maxPendingBytes)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var head struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return 0, err
	}
	return head.Version, nil
}

// Add records that step is owed. An entry for a step already in the document is
// replaced, so two changes that owe the same step leave one entry — the one
// that stays names the last change to ask for it. The steps dedupe and run
// once (spec rule 3, the global constraint).
func (p *Pending) Add(s PendingStep) {
	for i, have := range p.Steps {
		if have.Step == s.Step {
			p.Steps[i] = s
			return
		}
	}
	p.Steps = append(p.Steps, s)
}

// Clear drops the entries for the named steps and leaves the rest: `fugaro
// update` clears the stages it ran successfully and nothing else, so a catalog
// build it never runs stays owed (D1567).
func (p *Pending) Clear(names ...string) {
	p.Steps = slices.DeleteFunc(p.Steps, func(s PendingStep) bool {
		return slices.Contains(names, s.Step)
	})
}

// Names is the steps the document owes, deduped and in the order they run
// (Order): what `fugaro apply` runs and what `fugaro apply --status` prints.
func (p *Pending) Names() []string {
	names := make([]string, 0, len(p.Steps))
	for _, s := range p.Steps {
		names = append(names, s.Step)
	}
	return Order(names)
}

// writePendingFile writes data to path, mode 0600 in a 0700 directory, through
// a temporary file renamed over it. The temporary file is synced before the
// rename, so a crash cannot leave the new name pointing at a file whose
// contents never reached the disk (localcfg's writeCacheFile, for its reason:
// a zero-length state file is refused by the reader and would stay refused
// until someone deleted it).
func writePendingFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
