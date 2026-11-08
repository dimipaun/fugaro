package issues

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

// Source says where an Identity came from: the credential this run asked, the
// cache of the last answer a credential gave, or nothing at all.
type Source string

const (
	// SourceVerified is an address the credential answered with in this run.
	SourceVerified Source = "verified"
	// SourceCached is the last address a credential answered with, read back
	// offline. It is a display answer and never decides a write.
	SourceCached Source = "cached"
	// SourceNone is nobody: no credential could be read and no cache was
	// there.
	SourceNone Source = "none"
)

// Identity is who "me" is: the member string the credential belongs to, the
// digest the shared store's keys are built from, and where the answer came
// from.
type Identity struct {
	// Address is the member string exactly as access.Caller returns it
	// ("user:<address>"). It is never folded here: access.Member folds the
	// domain and keeps the local part byte for byte, because folding the local
	// part could merge two people into one reporter (F3).
	Address string
	// Digest is Digest(Address), empty when there is no address.
	Digest string
	// Source is where Address came from.
	Source Source
}

// Digest is the shared store's name for a reporter: the first 16 hex
// characters of the SHA-256 of the member string. It is hygiene and not
// secrecy — the address itself stays in the record and the occurrence bodies —
// and it is the one function the store's key layout and `mine` both build
// from, so the two sides can never disagree about where a reporter's issues
// are (F7).
func Digest(member string) string {
	sum := sha256.Sum256([]byte(member))
	return hex.EncodeToString(sum[:])[:16]
}

// Deps is what an identity lookup decides against: the seam that asks the
// credential who it belongs to, whether that seam may be called at all, and
// where the cache of the last answer lives.
type Deps struct {
	// Lookup is the seam: a command passes access.Caller, and a test passes
	// its own, so no test reaches Google's userinfo endpoint.
	Lookup func(ctx context.Context) (string, error)
	// NoLookup is set in a coding agent's session (F10): the identity comes
	// from the cache or is none, and the seam is never called.
	NoLookup bool
	// Getenv and GOOS place the cache. A command passes os.Getenv and
	// runtime.GOOS; localcfg.UserDir reads them.
	Getenv func(string) string
	GOOS   string
}

// identityLookupTimeout bounds the one lookup a run makes. access's own reader
// allows 20 seconds, which is a wait an offline `mine` must not spend before
// it prints the rows it already has (U3, Check 471).
const identityLookupTimeout = 5 * time.Second

// errNoVerifiedIdentity is a write's refusal when the seam may not be called
// or a coding agent's session has no credential to read.
var errNoVerifiedIdentity = errors.New("no verified credential to name the reporter with")

// ResolveIdentity answers who "me" is for a display read: the address the
// credential names when it can be read, and otherwise the last verified answer
// this machine cached. The cache is what makes the spec's offline acceptance
// work — a run with no network still knows who "me" is — and it never decides
// a write (VerifiedOnly is the entry a write uses).
//
// The error is the lookup's own when there is no answer at all, because the
// only way a real lookup leaves nobody is a lookup that failed; a coding
// agent's session (NoLookup) has no identity by design and no error.
func ResolveIdentity(ctx context.Context, deps Deps) (Identity, error) {
	if !canLookup(deps) {
		if id, ok := loadIdentityCache(deps); ok {
			return id, nil
		}
		return Identity{Source: SourceNone}, nil
	}
	member, err := lookupMember(ctx, deps)
	if err != nil {
		if id, ok := loadIdentityCache(deps); ok {
			return id, nil
		}
		return Identity{Source: SourceNone}, err
	}
	return verifiedIdentity(deps, member), nil
}

// VerifiedOnly is the entry an upload, a claim or a dismissal uses, and it
// refuses the cache: the cache is a display convenience a coding agent or
// anyone with the user's own files could write, and who a write names is not
// something it may decide (F3). A run whose credential cannot be read gets no
// identity and the lookup's error, so the issue stays local.
func VerifiedOnly(ctx context.Context, deps Deps) (Identity, error) {
	member, err := lookupMember(ctx, deps)
	if err != nil {
		return Identity{Source: SourceNone}, err
	}
	return verifiedIdentity(deps, member), nil
}

// canLookup reports whether the seam may be called at all.
func canLookup(deps Deps) bool { return !deps.NoLookup && deps.Lookup != nil }

// lookupMember asks the credential, under this package's own bound.
func lookupMember(ctx context.Context, deps Deps) (string, error) {
	if !canLookup(deps) {
		return "", errNoVerifiedIdentity
	}
	ctx, cancel := context.WithTimeout(ctx, identityLookupTimeout)
	defer cancel()
	member, err := deps.Lookup(ctx)
	if err != nil {
		return "", err
	}
	if member == "" {
		return "", errors.New("the credential named nobody")
	}
	return member, nil
}

// verifiedIdentity is the answer a credential gave, cached for the runs that
// cannot ask. The cache is best effort: the answer is in hand, and a machine
// whose cache cannot be written still knows who "me" is for this run.
func verifiedIdentity(deps Deps, member string) Identity {
	id := identityFrom(member, SourceVerified)
	saveIdentityCache(deps, id)
	return id
}

func identityFrom(member string, source Source) Identity {
	return Identity{Address: member, Digest: Digest(member), Source: source}
}

// identityCacheFormat is the version of the cache document. It is written so a
// future shape is refused rather than misread, and read strictly so a field
// this build does not know is not silently dropped.
const identityCacheFormat = 1

// identityCachePath is $XDG_CACHE_HOME/fugaro/identity.json, else under
// ~/.cache, the directory the other caches of this CLI live in. There is one
// file and not one per project: the address is the machine's, and an account
// switch is picked up by the next online run.
func identityCachePath(deps Deps) (string, error) {
	getenv, goos := deps.Getenv, deps.GOOS
	if getenv == nil {
		getenv = os.Getenv
	}
	if goos == "" {
		goos = runtime.GOOS
	}
	dir, err := localcfg.UserDir(getenv, goos, localcfg.DirCache)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "fugaro", "identity.json"), nil
}

// identityCacheDoc is the file's shape: the version, and the member string.
// The digest is not stored, it is computed from the address, so the two can
// never disagree.
type identityCacheDoc struct {
	Format  int    `json:"format"`
	Address string `json:"address"`
}

// maxIdentityCacheBytes bounds the read: a member string is short, and a file
// near this is not one.
const maxIdentityCacheBytes = 4 << 10

// loadIdentityCache is the last verified answer, when there is one. Anything
// wrong with the file — absent, unreadable, too big, not JSON, another format,
// a field this build does not know, an empty address — is a miss rather than
// an error: the cache is a convenience, and a read that failed because of it
// would hide rows a person can still be shown.
func loadIdentityCache(deps Deps) (Identity, bool) {
	path, err := identityCachePath(deps)
	if err != nil {
		return Identity{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) > maxIdentityCacheBytes {
		return Identity{}, false
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var doc identityCacheDoc
	if dec.Decode(&doc) != nil || doc.Format != identityCacheFormat || doc.Address == "" {
		return Identity{}, false
	}
	// Nothing may follow the document: a file carrying a second value is not
	// the one document this build writes.
	var trailing any
	if dec.Decode(&trailing) != io.EOF {
		return Identity{}, false
	}
	return identityFrom(doc.Address, SourceCached), true
}

// saveIdentityCache rewrites the cache after a verified lookup. It keeps the
// user's caches' rule: 0600 in a 0700 directory, written through a rename, so
// a reader sees the old file or the new one and never a part.
func saveIdentityCache(deps Deps, id Identity) {
	path, err := identityCachePath(deps)
	if err != nil {
		return
	}
	data, err := json.Marshal(identityCacheDoc{Format: identityCacheFormat, Address: id.Address})
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return
	}
}
