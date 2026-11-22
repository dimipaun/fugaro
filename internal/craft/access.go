package craft

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/dimipaun/fugaro/internal/config"
)

// The access contract's question and its answer (C1 (a), R1): for a stage, and
// for the task's grade where it names one (F38), which credential or pool the
// calls go out on, which members it has and how each of them is served, whether
// the credential was chosen or is the project's default account (F35), and the
// interface every reader of the retired auth mode is moved onto.
//
// # One seam (R1)
//
// Every reader of the auth mode is moved to one question, and this is it: no
// reader keeps a switch on four words. The types of the credential or of a
// pool's members are config's own four (A2), so what a call is served by is
// decided from the type and the serving path (config.CredentialServingPath) and
// never from an auth word.
//
// # The shape of the answer
//
// An Access is a credential or a pool and nothing else, and its Members are the
// credential itself for a credential — one entry — or the pool's members in the
// pool's order for a pool. The order is the order (config.PoolMembers): it is
// what failover walks.
//
// Chosen is F35's second condition: false means the credential is the project's
// DEFAULT ACCOUNT and nobody chose it, which is the one case a run may walk away
// from an exhausted subscription (CM11). An account a file or a launch named
// (agent.oauth_token, --token, a stage's account) is chosen, and so is every
// access that is not a subscription — Vertex, the project's key and the
// cascade's pool have no walk to be walked on.
//
// Cascade says the pool is the one the migration wrote for the project's cascade
// (D3064 as amended): its subscription members are served exactly as the cascade
// serves them today although the spread switch is off.

// The two kinds an access has. They are the two names a craft's access may
// resolve to — a declared credential, or a pool of them — and not a third
// vocabulary beside config's: what a name IS is answered by which of a layer's
// two blocks defines it (Resolve).
const (
	// AccessCredential is one credential.
	AccessCredential = "credential"
	// AccessPool is one pool, whose Members are its members in its own order.
	AccessPool = "pool"
)

// Access is what one stage's calls go out on, answered.
type Access struct {
	// Kind is AccessCredential or AccessPool.
	Kind string
	// Name is the name the project's config, the record and the wire all speak:
	// a credential's name, or a pool's.
	Name string
	// Members is the credential itself for a credential, and the pool's members
	// for a pool, each with its type and the path that serves it. A member of a
	// type nothing serves carries an empty Path (config.PoolMembers) rather than
	// being dropped: a caller that has to refuse it has the name to refuse it
	// with.
	Members []config.PoolMember
	// Chosen is whether the credential was chosen rather than being the
	// project's default account (F35).
	Chosen bool
	// Cascade is whether a pool carries the cascade's marker (D3064 as amended).
	Cascade bool
}

// IsPool is whether the access is a pool.
func (a Access) IsPool() bool { return a.Kind == AccessPool }

// Resolver answers the contract's question. It is what every reader of the auth
// mode is moved onto (R1), and its real implementation is CM1's (craft.ByReach,
// internal/craft/reach.go, which judges an unset access by what reaches the
// craft's model); until that lands, the stub in access_stub.go answers the
// migration's rule alone, so every side compiles and behaves as today.
//
// ok=false is the contract's "nothing here answers": a stage this vocabulary
// has no craft for, or a file that writes none of the retired keys. It is never
// a default access — a run with no answer is refused with the fix, never sent to
// Vertex (F49).
type Resolver interface {
	AccessFor(stage, grade string) (Access, bool)
}

// EnvAccess is the entry the launcher puts the resolved answer under in the
// execution's environment (C1 (b), R15 (3)).
const EnvAccess = config.EnvAccess

// ResolvedVersion is the version of the document EnvAccess carries. The writer
// and the reader are two binaries — a launcher and a job image — so the version
// is a member and a reader that meets another one refuses the document rather
// than reading the members it recognises as the whole of it.
const ResolvedVersion = 1

// Resolved is what the launcher hands a run: each craft's answer and each
// grade's, keyed by the craft and the grade name the config speaks. It is the
// same answer Access is, and the launcher and the runner exchange it as
// EnvAccess's JSON so that neither has to read the other's configuration.
type Resolved struct {
	Version int               `json:"version"`
	Crafts  map[string]Access `json:"crafts,omitempty"`
	Grades  map[string]Access `json:"grades,omitempty"`
}

// The document's own shape: what an Access looks like on the wire. It is not
// Access itself because Access's members are config.PoolMember, which carries no
// wire spelling — and the contract's encoding is the contract's to state.
type resolvedDoc struct {
	Version int                  `json:"version"`
	Crafts  map[string]accessDoc `json:"crafts,omitempty"`
	Grades  map[string]accessDoc `json:"grades,omitempty"`
}

type accessDoc struct {
	Kind    string      `json:"kind"`
	Name    string      `json:"name"`
	Members []memberDoc `json:"members,omitempty"`
	Chosen  bool        `json:"chosen"`
	Cascade bool        `json:"cascade,omitempty"`
}

type memberDoc struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Path string `json:"path,omitempty"`
}

// The document's refusals, as sentinels so a caller can answer without reading
// a message. Neither carries a value.
var (
	// ErrResolvedVersion is a document written by a release this one does not
	// know.
	ErrResolvedVersion = errors.New("craft: the handed access was written by another version of fugaro")
	// ErrResolvedMalformed is a document that is not the answer it says it is:
	// not JSON, a craft or a grade this vocabulary does not hold, a kind that is
	// neither of the two, a name that is empty, or a pool that names no member —
	// answers no reader could decide from.
	ErrResolvedMalformed = errors.New("craft: the handed access is not the resolved access this release writes")
)

// Encode is the document as the launcher writes it into the environment entry:
// JSON, and nothing else. It refuses to encode an answer it could not read back,
// because the launcher hands a run what this document holds and a document this
// release would refuse is one the run would fall back from — silently, and to a
// rule about a file the launcher may already have replaced.
func (r Resolved) Encode() (string, error) {
	if r.Version == 0 {
		r.Version = ResolvedVersion
	}
	if err := r.check(); err != nil {
		return "", err
	}
	doc := resolvedDoc{Version: r.Version}
	if len(r.Crafts) > 0 {
		doc.Crafts = make(map[string]accessDoc, len(r.Crafts))
		for name, a := range r.Crafts {
			doc.Crafts[name] = a.doc()
		}
	}
	if len(r.Grades) > 0 {
		doc.Grades = make(map[string]accessDoc, len(r.Grades))
		for name, a := range r.Grades {
			doc.Grades[name] = a.doc()
		}
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("craft: encoding the resolved access: %w", err)
	}
	return string(out), nil
}

// ParseResolved reads the entry back. found=false is a run whose launcher put
// nothing there — an older launcher, or a repository no update has migrated —
// and it is not an error: the caller applies the migration's rule to the file
// (LegacyAccess, R15 (3)).
//
// A document that is present but unreadable IS an error, and so is a version
// this release does not know: a document half understood is one that could send
// a stage's calls to something the launcher never resolved.
func ParseResolved(v string) (Resolved, bool, error) {
	if v == "" {
		return Resolved{}, false, nil
	}
	var doc resolvedDoc
	dec := json.NewDecoder(bytes.NewReader([]byte(v)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return Resolved{}, false, fmt.Errorf("craft: reading the handed access: %w: %w", ErrResolvedMalformed, err)
	}
	if doc.Version != ResolvedVersion {
		return Resolved{}, false, fmt.Errorf("craft: reading the handed access: %w (version %d)", ErrResolvedVersion, doc.Version)
	}
	r := Resolved{Version: doc.Version}
	if len(doc.Crafts) > 0 {
		r.Crafts = make(map[string]Access, len(doc.Crafts))
		for name, a := range doc.Crafts {
			if !slices.Contains(config.CraftNames, name) {
				return Resolved{}, false, fmt.Errorf("craft: reading the handed access: %w (not a craft)", ErrResolvedMalformed)
			}
			access, err := a.access()
			if err != nil {
				return Resolved{}, false, err
			}
			r.Crafts[name] = access
		}
	}
	if len(doc.Grades) > 0 {
		r.Grades = make(map[string]Access, len(doc.Grades))
		for name, a := range doc.Grades {
			if !slices.Contains(config.GradeNames, name) {
				return Resolved{}, false, fmt.Errorf("craft: reading the handed access: %w (not a grade)", ErrResolvedMalformed)
			}
			access, err := a.access()
			if err != nil {
				return Resolved{}, false, err
			}
			r.Grades[name] = access
		}
	}
	return r, true, nil
}

// check refuses a document this release could not read back, which is the one
// thing Encode has to hold: the launcher writes it and the run reads it, so an
// answer that survives the round trip is the whole of what the writer owes.
func (r Resolved) check() error {
	if r.Version != ResolvedVersion {
		return fmt.Errorf("%w (version %d)", ErrResolvedVersion, r.Version)
	}
	for name, a := range r.Crafts {
		if !slices.Contains(config.CraftNames, name) {
			return fmt.Errorf("%w (not a craft)", ErrResolvedMalformed)
		}
		if err := a.check(); err != nil {
			return err
		}
	}
	for name, a := range r.Grades {
		if !slices.Contains(config.GradeNames, name) {
			return fmt.Errorf("%w (not a grade)", ErrResolvedMalformed)
		}
		if err := a.check(); err != nil {
			return err
		}
	}
	return nil
}

// check refuses an answer no reader could decide from, and it is the same rule
// on both sides of the entry: a plain credential whose name is empty names
// nothing, a kind that is neither of the two names no block of a layer, and a
// pool with no member is one neither path can walk.
func (a Access) check() error {
	if a.Kind != AccessCredential && a.Kind != AccessPool {
		return fmt.Errorf("%w (kind)", ErrResolvedMalformed)
	}
	if a.Name == "" {
		return fmt.Errorf("%w (name)", ErrResolvedMalformed)
	}
	if a.Kind == AccessCredential && a.Cascade {
		return fmt.Errorf("%w (the cascade's marker is a pool's)", ErrResolvedMalformed)
	}
	if a.Kind == AccessPool && len(a.Members) == 0 {
		return fmt.Errorf("%w (a pool names no member)", ErrResolvedMalformed)
	}
	return nil
}

func (a Access) doc() accessDoc {
	doc := accessDoc{Kind: a.Kind, Name: a.Name, Chosen: a.Chosen, Cascade: a.Cascade}
	for _, m := range a.Members {
		doc.Members = append(doc.Members, memberDoc{Name: m.Name, Type: m.Type, Path: m.Path})
	}
	return doc
}

func (d accessDoc) access() (Access, error) {
	a := Access{Kind: d.Kind, Name: d.Name, Chosen: d.Chosen, Cascade: d.Cascade}
	for _, m := range d.Members {
		a.Members = append(a.Members, config.PoolMember{Name: m.Name, Type: m.Type, Path: m.Path})
	}
	if err := a.check(); err != nil {
		return Access{}, fmt.Errorf("craft: reading the handed access: %w", err)
	}
	return a, nil
}
