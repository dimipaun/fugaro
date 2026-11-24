package runindex

import "context"

// stub is the Reader the run-index and indexed-reads tasks compile against
// until I-INDEX wires the real one in.
//
// It reads nothing at all -- no Firestore, no RTDB, no bucket -- and answers
// the same two things every time: an empty page, and a summary that says the
// index is not complete. That is deliberately not "an empty index": a caller
// that has wired nothing must not print an empty list as the truth, and
// IndexComplete false is what tells every reader to say the index is still
// building instead (IR1's TestRunsLsSaysIndexBuilding, and the constraint the
// plan holds over every reader: a reader never prints a silently short list
// while the index is incomplete).
//
// It is why I-INDEX's red step is a request count: with this wired in, a
// command reads no index at all, whatever the fake behind it holds.
func NewStub() Reader { return stub{} }

type stub struct{}

func (stub) List(context.Context, Filter) (Page, error) {
	return Page{Entries: []Entry{}}, nil
}

func (stub) Summary(context.Context) (Summary, error) {
	return Summary{Version: Version, States: map[string]int64{}}, nil
}
