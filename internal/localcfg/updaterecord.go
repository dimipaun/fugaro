package localcfg

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
)

// The record of the last update: what the last plain `fugaro update` retired
// by itself, and how many runs were in flight while it did
// (docs/console-managed-screens/reconciliation.md F20, F21, D1484, D1485).
//
// It exists because the retirement is a decision a run makes and then applies,
// and nothing else on this machine remembers it afterwards: the summary is
// printed once, the plan file is overwritten by the next run, and the state
// directory holds only where the checkouts are. The console's Project tab draws
// what the last update retired and the runs it walked past to do it, so the
// last plain update has to leave the answer behind
// (docs/console-managed-screens/implementation-plan.md CM1).
//
// It is state, in the sense and in the directory of the remembered checkouts
// (CheckoutsDir), for the reasons that file's own comment gives: it is written
// best effort — a state write that fails never fails the command that would
// have recorded it — its reader never trusts it, and it is never published to
// the runs bucket, never merged into the shared config and never read by a
// command the console does not spawn. It is NOT the local project config: that
// is a file a person edits by hand, and a retirement set is a list, which is
// not the shape of the yamledit writer behind policyGenerationRecord.

// UpdateRecordVersion is the version of the record document. A file whose
// version is not this one is refused rather than read as this version's (and
// never written over), so a newer fugaro's record survives an older one —
// CheckoutsVersion's rule, for its own reason: this file is state a newer build
// may have written in a shape this one would misread.
const UpdateRecordVersion = 1

// UpdateRecordFile is the record's name, inside CheckoutsDir. It is a document
// of its own beside checkouts.json rather than a member of it: the two are
// written by different commands, and a document per subject is what keeps one
// writer from dropping the other's state.
const UpdateRecordFile = "update.json"

// maxUpdateRecordBytes bounds the read: a retirement costs a few hundred bytes,
// and a project's set is bounded by the addresses its state can hold, so
// anything near this is not one. It is the checkouts bound, for its reason.
const maxUpdateRecordBytes = 1 << 20

// UpdateRetirement is one retired address as the record holds it, and it is
// infra.Retirement's own shape — member for member, name for name — because it
// has to be a copy: internal/infra imports this package (the retirement reads
// the local config), so this package cannot name that type. internal/cli
// converts one into the other and has a test that the two documents are byte
// for byte the same, so a member added, renamed or retyped there fails there
// rather than reaching the console as a row it cannot read. The nine members
// are also the members of the run's own `--json` rows (initResult.Retire),
// which is what makes the record a document the console already knows how to
// draw (D1485).
//
// infra.Retirement's own ProtectedRead is deliberately not among them: whether
// the state's deletion_protection was read is the refusal gate's input, not
// something the run reports.
type UpdateRetirement struct {
	Address   string `json:"address"`
	Kind      string `json:"kind"`
	Cause     string `json:"cause"`
	Kept      string `json:"kept,omitempty"`
	Logical   string `json:"logical,omitempty"`
	SecretID  string `json:"secret_id,omitempty"`
	Job       string `json:"job,omitempty"`
	Region    string `json:"region,omitempty"`
	Protected bool   `json:"protected"`
}

// UpdateProject is one project's record: when its last update settled its
// retirement, what that update retired, and — when the read could answer — how
// many runs the registry held while it did.
type UpdateProject struct {
	// At is when the run settled its retirement: the moment it found nothing
	// to retire, removed the protected addresses itself, or applied the plan
	// that deleted them. It is not the end of the command, which may go on to
	// build images and write the local config: this record's subject is the
	// retirement, and At dates that.
	At time.Time `json:"at"`
	// Retire is what that run retired, in the run's own order. It is written
	// even when it is empty: `[]` is "the last update retired nothing", which
	// is a different answer from a record still carrying an earlier update's
	// rows — the run that retired nothing replaces them.
	Retire []UpdateRetirement `json:"retire"`
	// RunsInFlight is how many runs the registry held when the retirement was
	// settled, and it is absent when the number is not known — the project has
	// no budget backend, or the read did not answer. Absent and never zero:
	// the console draws this number beside the retirement (F20), and a run that
	// could not be counted must not be drawn as a run that was not there
	// (D1483).
	RunsInFlight *int `json:"runs_in_flight,omitempty"`
}

// UpdateRecord is the whole document: the last update's retirement of each
// project this machine has run one for.
type UpdateRecord struct {
	Version int `json:"version"`
	// Projects maps a project name (config.ProjectNameRE, the same names
	// projects/<name>.yaml uses) to that project's last update.
	Projects map[string]UpdateProject `json:"projects"`
}

// UpdateRecordPath is where the record lives:
// $XDG_CONFIG_HOME/fugaro/state/update.json, else
// ~/.config/fugaro/state/update.json.
func UpdateRecordPath(getenv func(string) string) (string, error) {
	d, err := configDir(getenv)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, CheckoutsDir, UpdateRecordFile), nil
}

// errUpdateRecordVersion marks the one refusal RecordUpdate must not write
// over: a document a *newer* fugaro wrote.
var errUpdateRecordVersion = errors.New("update record is from another version of fugaro")

// EmptyUpdateRecord is the document for a machine that has recorded none.
func EmptyUpdateRecord() *UpdateRecord {
	return &UpdateRecord{Version: UpdateRecordVersion, Projects: map[string]UpdateProject{}}
}

// LoadUpdateRecord reads the record. A machine that has none (no file, or one
// that was deleted) is an empty document and no error; a file that cannot be
// read, is larger than the bound, is not a regular file (a symlink, a FIFO, a
// directory), is not JSON, or is not this version, is an error — LoadCheckouts'
// own rule, for its own reason: a reader that took a corrupt file for an empty
// one would then write over it.
func LoadUpdateRecord(getenv func(string) string) (*UpdateRecord, error) {
	path, err := UpdateRecordPath(getenv)
	if err != nil {
		return nil, err
	}
	data, err := config.ReadRegular(path, maxUpdateRecordBytes)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return EmptyUpdateRecord(), nil
	case err != nil:
		return nil, err
	}
	var r UpdateRecord
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("%s is not an update record: %w", path, err)
	}
	if r.Version != UpdateRecordVersion {
		return nil, fmt.Errorf("%s is version %d of the update record, and this fugaro writes version %d: %w",
			path, r.Version, UpdateRecordVersion, errUpdateRecordVersion)
	}
	if r.Projects == nil {
		r.Projects = map[string]UpdateProject{}
	}
	return &r, nil
}

// RecordUpdate replaces project's entry with u and writes the document whole:
// the file of its own under the config home, 0600 in a directory created 0700,
// written through a rename (writeCacheFile), so a reader never sees half a
// document.
//
// The other projects' entries are kept: what this document holds is each
// project's own last update, and a run that updates one project knows nothing
// about the others.
func RecordUpdate(getenv func(string) string, project string, u UpdateProject) error {
	if !config.ProjectNameRE.MatchString(project) {
		return fmt.Errorf("%q is not a project name", project)
	}
	r, err := LoadUpdateRecord(getenv)
	if err != nil {
		if errors.Is(err, errUpdateRecordVersion) {
			// Never write over a document a newer fugaro wrote: this build
			// cannot read it, and rebuilding it from nothing would drop that
			// fugaro's record (RecordCheckout's own rule).
			return err
		}
		// Everything else the read refuses — JSON this build cannot parse, a
		// byte count over the bound, a file that is not a regular file — is
		// this machine's state gone wrong, and this file is pure state: it is
		// dropped and rebuilt rather than left refusing every later write.
		r = EmptyUpdateRecord()
	}
	if u.Retire == nil {
		// An empty set is written as `[]`: see UpdateProject.Retire.
		u.Retire = []UpdateRetirement{}
	}
	r.Projects[project] = u
	path, err := UpdateRecordPath(getenv)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return writeCacheFile(path, append(data, '\n'))
}
