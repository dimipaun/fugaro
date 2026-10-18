package runstore

// The bound on the changed-file set a run records (console v2 design D27,
// F17). A run can change any number of paths, and the record is a small
// object in the runs bucket that every listing reads, so the set is capped:
// at MaxChangedFiles paths, or at MaxChangedPathBytes bytes of path, whichever
// comes first. A truncated set is never presented as a whole one — Total says
// how many paths there really were and Truncated says the stored list is
// short of it.
const (
	// MaxChangedFiles is how many paths a run's record stores.
	MaxChangedFiles = 200
	// MaxChangedPathBytes caps the stored paths' total length, so a plan that
	// renames a directory cannot put a megabyte of paths in a record.
	MaxChangedPathBytes = 16 << 10
)

// ChangedFiles is the set of paths a run changed, as the run itself recorded
// it (console v2 design D27, F17). It is paths and nothing else: no contents,
// no diff, no commit message, and no line counts — a path can name a client
// or a person, so the record holds the least that answers "did these two runs
// touch the same file".
//
// Paths is never null: an empty array is a fact ("this run changed no path")
// and a null is not. Total is how many paths the run changed, so a truncated
// record still says how big the set it is a part of was, and Truncated is set
// only when Paths is shorter than Total.
type ChangedFiles struct {
	Paths     []string `json:"paths"`
	Total     int      `json:"total"`
	Truncated bool     `json:"truncated,omitempty"`
}

// NewChangedFiles bounds paths and returns the set to record. Paths are kept
// in the order git gave them (sorted), so two records of the same work are
// the same record, and the cap falls on the same paths every time.
func NewChangedFiles(paths []string) *ChangedFiles {
	return boundChangedFiles(paths)
}

// Bounded is cf as a reader may show it. The record is an object the job's
// service account can write, so the set read back is not trusted to be the
// bounded, honest one NewChangedFiles wrote: it is bounded again (empty paths
// dropped, the same count and byte caps), Total is never less than the paths
// it read nor negative, and the set is marked truncated when the record said
// so or when what is kept is short of Total. nil stays nil: a record with no
// set is "no record", never an empty set.
func (cf *ChangedFiles) Bounded() *ChangedFiles {
	if cf == nil {
		return nil
	}
	kept := make([]string, 0, min(len(cf.Paths), MaxChangedFiles))
	for _, p := range cf.Paths {
		if p != "" {
			kept = append(kept, p)
		}
	}
	out := boundChangedFiles(kept)
	out.Total = max(out.Total, cf.Total)
	out.Truncated = out.Truncated || cf.Truncated || len(out.Paths) < out.Total
	return out
}

func boundChangedFiles(paths []string) *ChangedFiles {
	cf := &ChangedFiles{Paths: []string{}, Total: len(paths)}
	var bytes int
	for _, p := range paths {
		if len(cf.Paths) >= MaxChangedFiles || bytes+len(p) > MaxChangedPathBytes {
			cf.Truncated = true
			break
		}
		bytes += len(p)
		cf.Paths = append(cf.Paths, p)
	}
	return cf
}
