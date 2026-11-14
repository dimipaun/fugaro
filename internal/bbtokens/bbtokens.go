// Package bbtokens is the token store's contract (C1 of
// docs/bitbucket-token-types/implementation-plan.md): what every side of a
// Bitbucket token may rely on. The operator's record, the service's status
// beside it, the one snapshot a request is decided and served from, and the
// three interfaces the selection, the project lookup and the status are asked
// through.
//
// Nothing here reads a policy, a bundle, the state store or Bitbucket. The
// implementations live in the tasks that own them (select.go T4,
// projectlookup.go T5, verify.go T7, the service's wiring I1), and this package
// states the shapes alone so that those tasks can be written against one
// another in parallel.
//
// It imports nothing from internal/creds: the status of a Bitbucket token lives
// in the creds state store (T6), and the run side must reach neither the store
// nor the package that reads it (TestCredsIsNotReachableFromARun). The node's
// own shape and the identity set are declared in internal/projectpolicy, so
// the runner and the Bitbucket adapter import no token-handling code at all.
package bbtokens

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// Name is a token's document key: the bundle's bitbucket-token--<name> family,
// or the legacy bitbucket-token. Everything that names a token — the audit
// line, the status a mark is stored under, a `creds set` prompt — names it by
// this and never by the bare suffix, so no name can collide with a model
// account or with the legacy record (R5).
type Name string

// LegacyName is the document key of the one untyped token an installation that
// predates the family holds. It takes part in selection as an implicit record
// whose coverage is the operator's allow_data_to list, ranked after every typed
// record (R3, R14), so the legacy key ranks last and an installation that never
// stored a typed token behaves as it did before this work.
const LegacyName Name = "bitbucket-token"

// Type is what a token is scoped to (R1). It is redundant for matching — the
// coverage patterns and the project UUID are what match — and is stored for
// display and for the add-time check, which needs to know which call to make.
type Type string

const (
	// TypeRepo is a repository access token, covering one workspace/repo.
	TypeRepo Type = "repo"
	// TypeProject is a project access token, covering the repositories of one
	// project of one workspace.
	TypeProject Type = "project"
	// TypeWorkspace is a workspace access token, covering a whole workspace.
	TypeWorkspace Type = "workspace"
	// TypeUntyped is the legacy bitbucket-token key, whose coverage is the
	// operator's allow_data_to list (R3).
	TypeUntyped Type = ""
)

// Record is one token's operator-stated facts (D2350, D2351). It carries no
// value: the value is the bundle's (T1), and it is found by Name.
type Record struct {
	// Name is the token's document key.
	Name Name
	// Type is the token's scope: TypeRepo, TypeProject, TypeWorkspace, or
	// TypeUntyped for the implicit record the legacy key is (R3).
	Type Type
	// Coverage are the patterns the token may serve, in the LOCAL CONFIG's
	// allow_data_to spelling and not the node's: two segments, workspace/name,
	// written with the characters a repository name is made of and with the
	// workspace literal, exactly as config's invalidAllowDataToEntry holds an
	// allow_data_to entry (D464). T3 is what provider-qualifies them into the
	// node's three-segment allow_repos spelling.
	//
	// It says which token CAN serve a repository and never widens what a run
	// may reach, which stays the operator's allow_data_to (D2354).
	Coverage []string
	// ProjectUUID is the project a TypeProject token is scoped to. It is the
	// stable identifier and the value matching compares (R1).
	ProjectUUID string
	// ProjectKey is the operator's own spelling of that project, kept for
	// display: a key can be renamed and a UUID cannot.
	ProjectKey string
	// Expires is the day the token stops working, in the local config's
	// 2006-01-02 spelling, and empty when the operator recorded none. It is
	// the one expiry source for a typed token (R6).
	Expires string
}

// Validate reports a record whose type and coverage disagree, which is refused
// wherever a record is written or read rather than resolved in the selector's
// favour (R1).
func (r Record) Validate() error {
	switch r.Type {
	case TypeRepo:
		if len(r.Coverage) == 0 {
			return fmt.Errorf("the repository token %s covers no repository", r.Name)
		}
		for _, c := range r.Coverage {
			if _, name, err := coverageSegments(c); err != nil {
				return fmt.Errorf("the repository token %s covers %q, which is not a workspace/repo pattern: %w", r.Name, c, err)
			} else if strings.Contains(name, "*") {
				return fmt.Errorf("the repository token %s covers %q, which names more than one repository; a wildcard segment is a workspace token", r.Name, c)
			}
		}
		if r.ProjectUUID != "" {
			return fmt.Errorf("the repository token %s names a project UUID, which only a project token has", r.Name)
		}
	case TypeWorkspace:
		if err := coversOneWorkspace(r); err != nil {
			return err
		}
		if r.ProjectUUID != "" {
			return fmt.Errorf("the workspace token %s names a project UUID, which only a project token has", r.Name)
		}
	case TypeProject:
		if err := coversOneWorkspace(r); err != nil {
			return err
		}
		if r.ProjectUUID == "" {
			return fmt.Errorf("the project token %s names no project UUID, and a project key is display only", r.Name)
		}
	case TypeUntyped:
		for _, c := range r.Coverage {
			if _, _, err := coverageSegments(c); err != nil {
				return fmt.Errorf("the untyped token %s covers %q: %w", r.Name, c, err)
			}
		}
		if r.ProjectUUID != "" {
			return fmt.Errorf("the untyped token %s names a project UUID, which only a project token has", r.Name)
		}
	default:
		return fmt.Errorf("the Bitbucket token %s has the unknown type %q", r.Name, r.Type)
	}
	return nil
}

// coversOneWorkspace is the form a project and a workspace token share: the
// patterns all name the whole of one workspace, and the UUID is what tells the
// two apart.
func coversOneWorkspace(r Record) error {
	if len(r.Coverage) == 0 {
		return fmt.Errorf("the %s token %s covers no workspace", r.Type, r.Name)
	}
	for _, c := range r.Coverage {
		if _, name, err := coverageSegments(c); err != nil {
			return fmt.Errorf("the %s token %s covers %q, which is not a workspace/* pattern: %w", r.Type, r.Name, c, err)
		} else if name != "*" {
			return fmt.Errorf("the %s token %s covers %q, which does not name the whole workspace", r.Type, r.Name, c)
		}
	}
	return nil
}

// coverageSegmentRE is one segment of a coverage pattern: the characters a
// workspace or a repository name is made of, and "*" as a wildcard inside the
// segment. It is character for character config's own repoPatternSegmentRE
// (internal/config/providers.go), which is the copy an allow_data_to entry —
// the list a record's coverage is written beside in the operator's config — is
// held to.
var coverageSegmentRE = regexp.MustCompile(`^[A-Za-z0-9._*-]+$`)

// coverageSegments splits one coverage entry into its two segments, and reports
// an entry that is not one. It is D464's rule for an allow_data_to entry, the
// spelling a record's coverage is written in: exactly two segments of the
// characters above, neither empty nor a dot-path, and a LITERAL workspace —
// a wildcard workspace would make one token reach every workspace, which is the
// widening the operator's own list refuses. A wildcard *name* segment is left
// to the caller, because it is what tells a workspace token from a repository
// one.
func coverageSegments(c string) (workspace, name string, err error) {
	segs := strings.Split(c, "/")
	if len(segs) != 2 {
		return "", "", fmt.Errorf("%q is not a coverage pattern of the form workspace/name", c)
	}
	for _, s := range segs {
		if s == "" || s == "." || s == ".." || !coverageSegmentRE.MatchString(s) {
			return "", "", fmt.Errorf("%q is not a coverage pattern of the form workspace/name", c)
		}
	}
	if strings.Contains(segs[0], "*") {
		return "", "", fmt.Errorf("%q names a wildcard workspace, which reaches every workspace rather than one", c)
	}
	return segs[0], segs[1], nil
}

// MatchesProject reports whether the record is the project token for the UUID
// a repository's lookup answered. An empty UUID on either side never matches
// (R2): a record that states no project and a lookup that answered none are
// both "unknown", and treating two unknowns as equal would serve a project's
// repositories with a token that names no project at all.
func (r Record) MatchesProject(uuid string) bool {
	return r.Type == TypeProject && r.ProjectUUID != "" && uuid != "" && r.ProjectUUID == uuid
}

// State is how a token is doing. It speaks the vocabulary the creds state
// store's marks already use (F5): a Bitbucket token's status is stored beside
// the model credentials' and read by the same operators, so a second set of
// words for the same facts would be a second thing to keep in step.
type State string

const (
	// StateAvailable is a token nothing has marked.
	StateAvailable State = "available"
	// StateExhausted is a token Bitbucket answered 429 for, until Until
	// (D2356).
	StateExhausted State = "exhausted"
	// StateBackedOff is a 429 that named no Retry-After: a backoff of this
	// side's own, until Until.
	StateBackedOff State = "backed_off"
	// StateBroken is a token whose 401 a read of a repository it covers
	// confirmed (R4, R14). An operator clears it, as for a model credential.
	StateBroken State = "broken"
)

// Status is one token's state and the facts shown beside it. It is the
// non-secret half of a record, so it lives in the creds state store rather
// than in the local config a person can edit by hand (D2351, R12).
type Status struct {
	State State
	// Until is when StateExhausted or StateBackedOff lapses. The zero time is
	// "no end", which is what StateBroken and StateAvailable carry.
	Until time.Time
	// VerifiedAt is when the add-time check last accepted the token (R12).
	// The zero time is a token nothing has verified.
	VerifiedAt time.Time
	// Identity is the account UUID GET /2.0/user answered for the token, which
	// is what a comment's author is compared against (F20). It is empty for a
	// repository token, which that endpoint refuses (R14), so the set a run
	// trusts can be partial.
	Identity string
	// Count is the requests served with the token in the current window, kept
	// for the display D2356 asks for. It is advisory: nothing queues or refuses
	// a call on it.
	Count int
}

// Available reports a status that holds nothing. It reads the zero Status as
// available exactly as the model marks do (creds.Mark.Available): a token with
// no status node, one whose node says StateAvailable, and one whose mark was
// cleared are one thing, and no reader has to tell them apart — which is why
// StatusStore.Read may answer the zero Status for a token nothing has recorded.
func (s Status) Available() bool { return s.State == "" || s.State == StateAvailable }

// BundleTokens is the bundle's Bitbucket tokens as a snapshot holds them: the
// names the document holds and, for each, its value.
//
// The value is behind a method rather than a field, and a snapshot is what
// carries it, for the same reason a Bundle entry keeps its value unexported:
// the one path about to attach a token to a call is the only path that reads
// one, and nothing renders it. Names answers a stable order, so two reads of
// one document answer the same list.
type BundleTokens interface {
	// Names are the document keys of the Bitbucket tokens the bundle holds.
	Names() []Name
	// Value is the token named, and false when the bundle holds none.
	Value(name Name) (string, bool)
}

// Snapshot is one request's read of the two documents every step of it is
// decided from: the published policy and the bundle. Decide, the selection and
// the forward are handed the same one, so a request cannot be admitted against
// one policy and served under another, and a policy published between two reads
// of a request is never half-applied.
type Snapshot struct {
	// Policy is the shared policy node the request was admitted by. It is the
	// whole node rather than its bitbucket_token block alone, because the
	// legacy untyped record's coverage IS that block's allow_repos list (R3).
	Policy *projectpolicy.Policy
	// Bundle is the bundle's tokens, read once with the policy.
	Bundle BundleTokens
}

// Selector chooses the token one request is served with.
type Selector interface {
	// Select answers the narrowest usable token covering canonical, and a
	// *Refusal when none is (D2355). canonical is the canonical repository of
	// the PAIRING and not of the request: GET /user carries no repository, and
	// the contract fixes it from the calling job account for every call (U3).
	// write is true for a request that changes something upstream.
	Select(ctx context.Context, snap Snapshot, canonical string, write bool) (Choice, error)
}

// Choice is the token a request is served with.
type Choice struct {
	// Name is the token's document key. It is what the audit line writes
	// (R9), and never the token itself.
	Name Name
}

// ProjectResolver answers which project a repository is in, and forgets one it
// cached.
type ProjectResolver interface {
	// ProjectOf answers the UUID of the project canonical is in, asked with
	// the candidate project token itself and never with a wider or a narrower
	// one (R2). An empty UUID with a nil error is "that token does not cover
	// this repository" — Bitbucket's 403 or 404 — which is an answer rather
	// than a failure.
	ProjectOf(ctx context.Context, canonical string, tok Name) (uuid string, err error)
	// Evict drops what is cached for canonical, so the next call asks again
	// (R2, R4).
	Evict(canonical string)
}

// StatusStore reads and writes a Bitbucket token's status, which lives under
// its own subtree of the creds state store so that the readers of the model
// marks never see it (T6, D2351).
type StatusStore interface {
	// Read answers the token's status. A token nothing has recorded is the
	// zero Status rather than an error — which Status.Available reads as
	// available, so an empty store is indistinguishable from a healthy one and
	// no caller has to spell the two spellings of "nothing is wrong" itself.
	Read(ctx context.Context, name Name) (Status, error)
	// Write records the token's status.
	Write(ctx context.Context, name Name, st Status) error
	// CountRequest records one request served with the token, for the display
	// D2356 asks for. It is best effort: a caller counts with it and ignores
	// the error rather than failing or delaying a call over a counter, and a
	// failed write under-reports a number no decision is made on.
	CountRequest(ctx context.Context, name Name) error
}

// RefusalClass is why a call was refused or no token could be chosen. It is one
// closed vocabulary every side shares, so the service's log line, the caller's
// answer and the operator's reading of an audit line speak the same words.
//
// A class names no token: an operator reads which token was used off the audit
// line, and a caller told that a token exists learns something about an
// installation it has no business reading (R9).
type RefusalClass string

const (
	// ClassNotCovered is a repository no token covers. The refusal names the
	// repository, never a token (T4, T8).
	ClassNotCovered RefusalClass = "token_not_covered"
	// ClassBroken is every covering token marked broken, which an operator has
	// to clear (R4).
	ClassBroken RefusalClass = "token_broken"
	// ClassThrottled is every covering token throttled (R4). The proxy answers
	// HTTP 429 with Retry-After for it (R14).
	ClassThrottled RefusalClass = "token_throttled"
	// ClassWriteScopeMissing is a write the serving token never proved, which
	// Bitbucket's own 403 on the write is what confirms: nothing is created in
	// the operator's workspace to prove a write scope up front (D2353).
	ClassWriteScopeMissing RefusalClass = "write_scope_missing"
	// ClassNoToken is a bundle holding no Bitbucket token at all, which is an
	// installation that stored none rather than a coverage gap. A run holds
	// none of its own, so this is a refusal and never a call forwarded without
	// a credential.
	ClassNoToken RefusalClass = "no_bitbucket_token"
)

// Refusal is a class and the facts it names. It is an error so that a
// selection's answer travels as one, and it is a *Refusal so that a caller
// reads the class off it rather than off its text.
type Refusal struct {
	Class RefusalClass
	// Repo is the canonical repository the refusal is about, named for
	// ClassNotCovered. It is empty for a class that is about the store rather
	// than about one repository.
	Repo string
}

func (r *Refusal) Error() string {
	switch r.Class {
	case ClassNotCovered:
		return fmt.Sprintf("no Bitbucket token covers %s", r.Repo)
	case ClassBroken:
		return "every Bitbucket token covering the repository is marked broken"
	case ClassThrottled:
		return "every Bitbucket token covering the repository is throttled"
	case ClassWriteScopeMissing:
		return "the Bitbucket token's write scope was not verified"
	case ClassNoToken:
		return "the bundle holds no Bitbucket token, and a proxied run holds none of its own"
	default:
		return fmt.Sprintf("the Bitbucket token store refused the call (%s)", r.Class)
	}
}
