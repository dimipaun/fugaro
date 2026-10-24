package coordinator

import (
	"context"
	"errors"
	"slices"
	"time"
)

// This file is P4 of docs/coordinator/implementation-plan.md: the report path,
// the writer every orchestrator declares its work through, and the
// coordinator's half of the central orchestrator plan's P9.
//
// One record, one writer, one shape. `fugaro coordinator report` is the
// transport today — the CLI, because there is no front door before P8 —
// internal/cli/run.go's launchRun calls this same writer so a launch declares
// itself without a second command, and `report_intent` (P8) is this writer
// behind the front door. So the record's shape is defined here, once, and no
// transport invents one of its own.
//
// Two properties are the part rather than niceties around it:
//
//   - The document's ID is derived from the CALLER and the DECLARATION and
//     from nothing else (IntentID): no clock, no counter, no random source.
//     The same declaration from the same caller twice is one document, which
//     is Q6(A)'s idempotency in finding #17's house idiom and the reason
//     report_intent needs no Idempotency-Key header.
//   - It takes nothing back. The record is written and returned as it was
//     written; nothing here reads a node, compares the declaration against
//     what is in flight, or issues advice. That is what keeps this part
//     advisory (spec §3, Q10(C) refused), and it is why the writer's whole
//     dependency is a write and a clock (TestReportReadsNothingBack).
//
// What a record carries is a DECLARATION and never an identity: DeclaredBy is
// the caller's own string — the local config's user:, else git config
// user.email, the same string a run's launch.json records (reconciliation
// finding #11) — and nothing here verifies it or authorises anything by it.
// The text itself (a branch, a path, a service) is the caller's, carried as
// data: the view reports it, and the injection rules of Q14(A) are why nothing
// may act on an instruction found in it.
//
// Where a record sits is D195's layout, and the repository is the PATH rather
// than a field: `coordinator/repos/<slug>/intents/<id>`, one node per
// repository, so the read grant of Q15(A) stays one prefix and the same
// declaration about two repositories is two documents.

// Intent is one declaration: what a caller is about to touch, and who says so.
type Intent struct {
	// DeclaredBy is who declares it. Required: a report nobody can be named
	// for is refused (ErrUnnamedCaller).
	DeclaredBy string `json:"declared_by"`
	// At is when the declaration was made, stamped by the writer's own clock
	// (Reporter.Now) in UTC. It is deliberately NOT part of the document's ID:
	// two reports of one declaration are one document whatever the clock says.
	At time.Time `json:"at"`
	// Branch is the branch the caller is about to work on, "" when it declares
	// none.
	Branch string `json:"branch,omitempty"`
	// Release is the target release the caller declares. A caller that names
	// none has the one its branch names read out of it (ReleaseOf), which is
	// the only place the view can read a target release out of a branch name.
	Release string `json:"release,omitempty"`
	// Files are the paths the caller is about to touch, and Services the
	// services, each sorted and deduplicated (Normalise). They are the
	// caller's own text, and an empty list is the answer "declares none" —
	// never a missing answer, and never a null a reader has to interpret.
	Files    []string `json:"files"`
	Services []string `json:"services"`
}

// The report path's two refusals. Each says what is missing, because a caller
// that has to guess which of its arguments was wrong is a caller that tries
// again with the same one.
var (
	// ErrUnnamedCaller refuses a report from a caller the project cannot
	// name. Every record the coordinator holds is somebody's declaration, and
	// one nobody can be named for is not a declaration.
	ErrUnnamedCaller = errors.New("an intent report must name who declares it: the local config's user:, else git config user.email")
	// ErrNothingDeclared refuses a report that declares nothing. A document
	// that says only "somebody was here" is a record the view has to read and
	// then discard, and it would collide with the next such report.
	ErrNothingDeclared = errors.New("an intent report must declare something: the branch, the release, a file or a service the caller is about to touch")
)

// Normalise returns the declaration as the report path records it: the file
// and service lists sorted, deduplicated and freed of empty entries, and the
// release read out of the branch when the caller named none.
//
// It is what makes two spellings of one declaration one document: the order a
// caller lists its paths in, and a path it names twice, are not part of what
// it declares, so they may not be part of the ID (IntentID).
func (in Intent) Normalise() Intent {
	in.Files = cleanList(in.Files)
	in.Services = cleanList(in.Services)
	if in.Release == "" {
		in.Release = ReleaseOf(in.Branch)
	}
	return in
}

// Validate refuses a report the coordinator could not hold: one from a caller
// it cannot name, and one that declares nothing. It is called on the
// normalised record, so a branch naming a release counts as the release too.
func (in Intent) Validate() error {
	switch {
	case in.DeclaredBy == "":
		return ErrUnnamedCaller
	case in.Branch == "" && in.Release == "" && len(in.Files) == 0 && len(in.Services) == 0:
		return ErrNothingDeclared
	}
	return nil
}

// IntentID is the database key of one intent record: a function of the caller
// and the declaration, and of nothing else. No clock, no counter and no random
// source goes in, so the same thing declared twice is the same key — which is
// how "the same event delivered twice is harmless" is satisfied without an
// Idempotency-Key header (finding #17).
//
// The repository is not one of the parts: the document sits under that
// repository's own node, so the path carries it and a second copy in the key
// would be a second thing to keep in step. Each part is named by kind before
// it is hashed, so a file and a service of one name are two declarations and
// not one.
//
// The answer is DocumentID's lower-case hex, which is also what makes the key
// safe: a branch or a path may hold a dot, a slash or a bracket, and none of
// them is representable in hex.
func IntentID(in Intent) string {
	in = in.Normalise()
	parts := []string{"intent", "by", in.DeclaredBy, "branch", in.Branch, "release", in.Release}
	for _, f := range in.Files {
		parts = append(parts, "file", f)
	}
	for _, s := range in.Services {
		parts = append(parts, "service", s)
	}
	return DocumentID(parts...)
}

// cleanList is a declaration's list as the record holds it: sorted,
// deduplicated, and with the empty entries dropped. A nil list answers with an
// empty one, so the record never carries a null where it means "none".
func cleanList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v != "" {
			out = append(out, v)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// Writer is the one thing the report path is handed: the coordinator's own
// write, and nothing else. There is no read in this interface, and that is the
// part's contract rather than what it happened to need — the report path takes
// nothing back, so it cannot compare a declaration against what is in flight
// and it cannot grow advice without a dependency it does not have (P4's third
// red step). *Store is a Writer: the record goes through the same ETag CAS
// every other write of the coordinator's state goes through.
type Writer interface {
	Write(ctx context.Context, path string, v any) error
}

// Reporter writes intent records. Its whole dependency list is the store it
// writes through and the clock it stamps with: no reader, no view, no model
// and no credential of its own.
type Reporter struct {
	// Store is where the record is written. Required.
	Store Writer
	// Now is the clock the declaration is stamped with; nil means time.Now.
	Now func() time.Time
}

// Report writes in as one intent record under slug's repository node, and
// returns the record as it was written and the path it was written to.
//
// The record is normalised and validated first, so a refused report writes
// nothing: a caller the project cannot name and a declaration that declares
// nothing are both refused before the database is touched. The write is the
// store's own read-then-CAS (Store.Write), so two callers reporting at once do
// not lose one another's document — and two reports of ONE declaration land on
// one document, because the path is a function of the declaration (IntentID).
//
// What it returns is the record this call built and not a node read back: the
// report path takes nothing back, and a caller that wanted to know what is
// already in flight would be asking for the view (P1/P2), which is a different
// reader and a different answer.
func (r *Reporter) Report(ctx context.Context, slug string, in Intent) (Intent, string, error) {
	if r.Store == nil {
		panic("coordinator: Reporter.Store is required; build it with NewStore")
	}
	in = in.Normalise()
	if err := in.Validate(); err != nil {
		return Intent{}, "", err
	}
	in.At = r.now().UTC()
	path := PathIntent(slug, IntentID(in))
	if err := r.Store.Write(ctx, path, in); err != nil {
		return Intent{}, "", err
	}
	return in, path, nil
}

func (r *Reporter) now() time.Time {
	if r.Now == nil {
		return time.Now()
	}
	return r.Now()
}
