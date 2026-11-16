// Listing what a machine and a database hold: the ids under `orchestrator/`,
// one id's lease node, and the cached briefs in the local cache directory.
//
// It is the registry end of D1358, which refuses a registry of PIDs and a
// local HTTP endpoint in favour of what the daemon already writes: the RTDB
// node is what every orchestrator of an installation has, and the cache file
// is the one this machine's daemon keeps. `fugaro orchestrator ls` (CG4 of
// docs/cli-output-console-graph) is the reader, and these are its reads.
//
// Nothing here holds a claim, writes, or reads a clock of its own: the lease
// node is read for what it says, and the caller — which has just read the
// database — decides what its stamp means.

package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// Keys is the shallow read a listing needs: the keys at one path, with no
// values. *rtdb.Client is one.
type Keys interface {
	Keys(ctx context.Context, path string) ([]string, error)
}

// NodeIDs returns the ids of the orchestrators whose node exists under
// `orchestrator/`, in order.
//
// One shallow request, not a read of the subtree: a state document is up to
// MaxStateBytes and an audit log is longer still, so listing the ids by
// downloading them is exactly the cost D1358's "read the node, not a registry"
// is not. A key this release cannot address is left out rather than listed as
// an orchestrator: every path under the subtree is built from an id, so a name
// PathState refuses is a node no reader of this release could have written and
// none can open.
func NodeIDs(ctx context.Context, db Keys) ([]string, error) {
	keys, err := db.Keys(ctx, SubtreeRoot)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(keys))
	for _, key := range keys {
		if err := checkAgentID(key); err != nil {
			continue
		}
		ids = append(ids, key)
	}
	return ids, nil
}

// LeaseClaim is one orchestrator's lease node as a listing reads it: who holds
// the id and the server time of that holder's last beat. The zero value is an
// id nobody holds, which is a state and not an error.
type LeaseClaim struct {
	Holder string
	At     time.Time
}

// ReadLeaseNode reads one id's lease node.
//
// It reads without a claim of its own and without a clock: Lease.Read answers
// "does **this** daemon hold the id", which is not the question a listing asks,
// and its freshness test needs the server's clock. The decode is the same
// strict one either end of this subtree uses, and a node this release cannot
// read is refused rather than reported as unheld — reading a broken lease as
// "nobody holds this id" is the reading that lets two daemons take one id
// (lease.go's interpret).
func ReadLeaseNode(ctx context.Context, db CAS, agentID string) (LeaseClaim, error) {
	path, err := PathLease(agentID)
	if err != nil {
		return LeaseClaim{}, err
	}
	var raw json.RawMessage
	_, found, err := db.GetETag(ctx, path, &raw)
	if err != nil {
		return LeaseClaim{}, err
	}
	if !found {
		return LeaseClaim{}, nil
	}
	doc, err := decodeStrict[leaseDoc](raw)
	if err != nil {
		return LeaseClaim{}, err
	}
	if !idRE.MatchString(doc.Holder) {
		return LeaseClaim{}, fmt.Errorf("%w: the lease names %q, which is not a holder", ErrInvalid, safetext.OneLine(doc.Holder))
	}
	return LeaseClaim{Holder: doc.Holder, At: stamp(doc.At)}, nil
}

// Fresh reports whether the claim is being beaten: somebody holds the id and
// its last beat is inside the lease's TTL, and not stamped so far ahead of now
// that this release would not honour it (Lease.Read's own rule, LeaseSkew).
// The zero claim — an id nobody holds — is never fresh.
//
// It is the liveness signal a listing reads, and it is deliberately not the age
// of a cached brief: a daemon beats on the lease's own cadence (LeaseBeat)
// whatever its cycles do, while the cache is written by a *successful* cycle
// only — `fugaro orchestrator run` accepts an --interval of the whole LeaseTTL,
// and a cycle that failed writes no cache at all while the beat goes on — so a
// cache older than a lease TTL is a daemon with a failed cycle, not a stopped
// one.
//
// What it cannot say is which machine holds the id: the holder is a daemon's
// own run ID, so a claim beaten elsewhere leaves this machine's cache file
// frozen while the id is driven. A listing shows the holder and its beat beside
// the cache's stamp, which is where that reads.
func (c LeaseClaim) Fresh(now time.Time) bool {
	if c.Holder == "" || c.At.IsZero() {
		return false
	}
	return now.Sub(c.At) < LeaseTTL && c.At.Sub(now) <= LeaseSkew
}

// CachedBrief is one cached brief of this machine: the orchestrator id, which
// is the file's own name, and the server time the daemon wrote it with.
type CachedBrief struct {
	ID string
	At time.Time
}

// CacheDir is the directory the daemon's cached briefs live in:
// $XDG_CACHE_HOME/fugaro/orchestrator. goos is a parameter for the reason
// localcfg.UserDir takes one.
func CacheDir(getenv func(string) string, goos string) (string, error) {
	dir, err := localcfg.UserDir(getenv, goos, localcfg.DirCache)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "fugaro", "orchestrator"), nil
}

// CachedBriefs lists the daemon's cached briefs on this machine, in order by
// id. No directory yet — a machine that has never run a daemon — has none, and
// that is an empty list rather than a refusal.
//
// A file whose name is not an orchestrator id is not one of these caches and is
// left out; the directory is the cache directory of every Fugaro command, and a
// temp file a write is in the middle of is exactly that. A file named like a
// cache that this release cannot read *is* refused: the reader's half of the
// writer's contract (cache.go) is strict about the shape and the version, so a
// cache from another release or a hand-edit is reported rather than quietly
// dropping an orchestrator the machine runs.
func CachedBriefs(getenv func(string) string, goos string) ([]CachedBrief, error) {
	dir, err := CacheDir(getenv, goos)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("listing the daemon's cached briefs at %s: %w", dir, err)
	}
	out := make([]CachedBrief, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		id, ok := strings.CutSuffix(name, ".json")
		if !ok || e.IsDir() || checkAgentID(id) != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("reading the daemon's cached brief at %s: %w", filepath.Join(dir, name), err)
		}
		c, err := decodeStrict[BriefCache](data)
		if err != nil {
			return nil, fmt.Errorf("the daemon's cached brief at %s is not a document this release reads: %w", filepath.Join(dir, name), err)
		}
		if c.Version != BriefCacheVersion {
			return nil, fmt.Errorf("the daemon's cached brief at %s carries schema_version %d, and this release reads %d", filepath.Join(dir, name), c.Version, BriefCacheVersion)
		}
		if c.WrittenAt.IsZero() {
			return nil, fmt.Errorf("the daemon's cached brief at %s carries no time it was written, so how fresh it is cannot be shown", filepath.Join(dir, name))
		}
		out = append(out, CachedBrief{ID: id, At: c.WrittenAt.UTC()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
