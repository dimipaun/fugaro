package runner

import (
	"errors"

	"github.com/dimipaun/fugaro/internal/craft"
)

// This file is the scaffolding of C1, and it is the one part of this package
// that goes away: I1 wires the real accessor (CM8's access_resolve.go) and
// deletes this file.
//
// What it is for is the run's own side of the contract being written later than
// the contract. A run learns each stage's access from the entry its launcher put
// in the execution's environment (craft.EnvAccess, R15 (3)); until the runner
// reads that entry, the accessor here gives it the two answers a run has TODAY:
// the entry's, when there is one, and the migration's rule over the retired
// vocabulary when there is not (craft.LegacyAccess, R3).
//
// It is one function and not a type because nothing about the run is decided in
// it: the caller holds the documents and hands them over, and the answer is the
// contract's.

// errStubAccess is what the accessor refuses with when nothing answers: no
// entry was handed to the run and its file writes none of the retired keys
// either (F49). The caller refuses the run with the fix — craft's
// LegacyNoAccessRefusal — and never sends it to Vertex; this is the error a
// developer sees while the real accessor is not wired yet, so it says which
// thing answered and nothing about the run.
var errStubAccess = errors.New("runner: the stub accessor answers the retired vocabulary alone (C1's scaffolding, deleted at I1)")

// stubAccess is the run's access for one stage, until CM8's accessor lands: the
// entry the launcher handed it where it carries one, and the migration's rule
// over in where it does not.
//
// handed is the raw value of craft.EnvAccess (R15 (3)). A handed document this
// release cannot read is an error and not a fall back to the rule: it was
// written by the launcher of the same release, so a document half understood is
// one that could send a stage's calls to something the launcher never resolved,
// and the rule reads a file the launcher may already have replaced.
func stubAccess(handed string, in craft.LegacyInput, stage, grade string) (craft.Access, error) {
	resolved, found, err := craft.ParseResolved(handed)
	if err != nil {
		return craft.Access{}, err
	}
	if found {
		name := stageCraft(stage)
		if name == "" {
			return craft.Access{}, errStubAccess
		}
		if grade != "" {
			if a, ok := resolved.Grades[grade]; ok {
				return a, nil
			}
		}
		if a, ok := resolved.Crafts[name]; ok {
			return a, nil
		}
		return craft.Access{}, errStubAccess
	}
	a, ok := craft.LegacyAccess(in, stage, grade)
	if !ok {
		return craft.Access{}, errStubAccess
	}
	return a, nil
}
