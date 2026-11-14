package creds

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
)

// RepoAuth is the record at creds/repoauth/<slug>: what one repository's
// agent.auth is, and — for oauth — the Claude accounts its workflows name.
//
// # Why a record exists at all
//
// A non-cascade repository's calls are served by the credentials service from
// the installation's bundle, and the bundle holds every account and the paid
// key alike: which of them a repository's own configuration says its runs go
// out on is not a fact the bundle carries (D2070 (a), R7). The run cannot be
// asked — the agent holds the run identity and the job's ID token, so it can
// send whatever it likes on the wire, and a statement the caller makes about
// itself is not a boundary (G16). The answer therefore has to be recorded
// where the caller cannot write it: `fugaro update` writes this node from the
// repository's own resolved config, and the service reads it on every call and
// narrows the caller's admission to it.
//
// # No value, ever
//
// No member of this record is a credential's value and none may ever be (G18):
// the auth is one of config.AgentAuths and an account is a name. The record is
// therefore safe to read, cache and log, and the closed shape below is what
// keeps it that way — a member that could carry a value is an edit a reviewer
// sees here and in the read, which refuses a member this release does not
// model.
//
// # Who may read it
//
// It lives under creds/, the subtree the database rules deny every run identity
// (O1, D136), and it is reached through a DB by two callers: the operator's CLI
// (internal/cli, which writes it) and the service (internal/credsvc, which
// decides from it). The record says which credential a repository's runs may be
// served, so a token that could WRITE it could widen that repository's reach to
// the whole bundle — the boundary internal/creds/boundary_test.go pins over the
// code half.
type RepoAuth struct {
	// Version is the format's version. It is a member rather than a constant
	// because the writer and the reader are two binaries (the CLI writes, the
	// service reads), and a reader that meets a version it does not know
	// refuses the record rather than reading the members it recognises as the
	// whole of it.
	Version int `json:"version"`
	// Auth is the repository's agent.auth, one of config.AgentAuths. A
	// repository whose runs the service may serve is one whose record says so:
	// `cascade` is the cascade as it is today, and every other value is a
	// declaration of what that repository's calls are served BY — which the
	// service narrows to rather than trusting the caller's own statement of it.
	Auth string `json:"auth"`
	// Accounts are the Claude accounts the repository's workflows name, sorted
	// and without repeats, each spelled as config.ClaudeAccountNameRE spells a
	// name (config.DefaultClaudeAccount for the unsuffixed account). It is
	// empty for every auth but `oauth`: a repository that does not go out on a
	// subscription names no account, and a record that named one anyway would
	// be a record whose shape implies a reach nothing in the repository's own
	// configuration asked for.
	Accounts []string `json:"accounts"`
}

const (
	// pathRepoAuth is the family under creds/ the records live in, one node per
	// repository slug.
	pathRepoAuth = "creds/repoauth"

	// RepoAuthVersion is the version this release writes and reads.
	RepoAuthVersion = 1

	// authOAuth is agent.auth's subscription value (config.AgentAuths), the one
	// auth a record may name accounts under.
	authOAuth = "oauth"
)

// PathRepoAuth is one repository's record. It panics on an empty slug, as every
// path builder in this package does: a slug reaches it from a task or a spec,
// never from a request.
func PathRepoAuth(slug string) string {
	return pathRepoAuth + "/" + seg("slug", slug)
}

// The record's refusals, as sentinels so a caller can answer without reading a
// message. Neither carries a value or a caller's own text.
var (
	// ErrNoRepoAuth is a slug that names no repository: no path is built from
	// it (an empty segment would collapse onto another node).
	ErrNoRepoAuth = errors.New("creds: the repository auth record names no repository")
	// ErrRepoAuthMalformed is a record that could not be written as it stands:
	// an auth that is not one config.AgentAuths holds, an account under an auth
	// that names none, or an account outside the shape a Claude account's name
	// has — an answer no reader could decide from.
	ErrRepoAuthMalformed = errors.New("creds: a repository auth record must name one known auth, and the accounts of a subscription repository, once each")
	// ErrRepoAuthVersion is a record written by a release this one does not
	// know. It is refused rather than half read: the members this release
	// recognises are not the whole of a document it did not write.
	ErrRepoAuthVersion = errors.New("creds: the repository auth record was written by another version of fugaro")
)

// ReadRepoAuth reads one repository's record. found=false is a repository no
// update has recorded — an installation that has not run the release that
// writes these — and it is not an error: the caller decides what an absent
// record means, and for the service that is "the cascade, unchanged" (D2070
// (a): an installation with no record is one this release has not narrowed).
//
// A node that is present but unreadable IS an error, and so is a version this
// release does not know: a record half understood is one that could say a
// repository goes out on the cascade when its own configuration says otherwise.
func ReadRepoAuth(ctx context.Context, db DB, slug string) (RepoAuth, bool, error) {
	if slug == "" {
		return RepoAuth{}, false, ErrNoRepoAuth
	}
	path := PathRepoAuth(slug)
	var raw json.RawMessage
	_, found, err := db.GetETag(ctx, path, &raw)
	if err != nil {
		return RepoAuth{}, false, fmt.Errorf("creds: reading %s: %w", path, err)
	}
	if !found || len(raw) == 0 {
		return RepoAuth{}, false, nil
	}
	rec, err := decodeStrict[RepoAuth](raw)
	if err != nil {
		return RepoAuth{}, false, fmt.Errorf("creds: reading %s: %w", path, err)
	}
	if rec.Version != RepoAuthVersion {
		return RepoAuth{}, false, fmt.Errorf("creds: reading %s: %w (version %d)", path, ErrRepoAuthVersion, rec.Version)
	}
	return rec, true, nil
}

// PutRepoAuth writes one repository's record whole, replacing whatever the node
// held. It is a whole replacement and not a merge: the record says what the
// repository's configuration says NOW, so an account a workflow dropped has to
// go — a merge would leave the service serving an account the repository
// stopped naming, which is the reach this record exists to bound.
//
// The write is conditional on the value read (cas), so a concurrent update of
// the same repository leaves one of the two answers rather than a torn one, and
// a node whose version member is not this release's — one a newer fugaro wrote
// — is refused instead of clobbered: fail closed at the writer, where the
// mistake would otherwise become a record nothing can be decided from. The
// version member exists because the writer and the reader are two binaries, so
// the check belongs where the record is replaced.
//
// The accounts are put in name order here rather than left to the caller, so
// the node is byte-identical for the same configuration however the workflows
// were listed, and the version is the writer's for the same reason: the format
// is this release's, and a caller does not get to name another one. An entry
// that is the empty string is written as config.DefaultClaudeAccount, which is
// the spelling the bundle's tier-A credential carries for the unsuffixed
// account: the axis a repository states its account on writes the default as
// the empty string, and a record that kept that spelling would name an account
// the service compares against nothing.
func PutRepoAuth(ctx context.Context, db DB, slug string, rec RepoAuth) error {
	if slug == "" {
		return ErrNoRepoAuth
	}
	if !config.KnownAgentAuth(rec.Auth) {
		return ErrRepoAuthMalformed
	}
	next := RepoAuth{Version: RepoAuthVersion, Auth: rec.Auth}
	if len(rec.Accounts) > 0 && rec.Auth != authOAuth {
		return ErrRepoAuthMalformed
	}
	if len(rec.Accounts) > 0 {
		rows := make([]string, 0, len(rec.Accounts))
		for _, a := range rec.Accounts {
			a = config.ReviewVariantAccount(a)
			if a != config.DefaultClaudeAccount &&
				(len(a) > config.MaxClaudeAccountNameLen || !config.ClaudeAccountNameRE.MatchString(a)) {
				return ErrRepoAuthMalformed
			}
			rows = append(rows, a)
		}
		slices.Sort(rows)
		for i := 1; i < len(rows); i++ {
			if rows[i-1] == rows[i] {
				return ErrRepoAuthMalformed
			}
		}
		next.Accounts = rows
	}
	path := PathRepoAuth(slug)
	_, err := cas(ctx, db, path, DefaultAttempts, func(cur RepoAuth, found bool) (RepoAuth, error) {
		if found && cur.Version != RepoAuthVersion {
			return RepoAuth{}, fmt.Errorf("%w (version %d)", ErrRepoAuthVersion, cur.Version)
		}
		return next, nil
	})
	if err != nil {
		return fmt.Errorf("creds: writing %s: %w", path, err)
	}
	return nil
}

// NamesAccount is whether the record names that Claude account, which is the
// whole of what a non-cascade oauth repository's call may be served by. It is a
// method rather than a set the caller builds so that the comparison is spelled
// once, in the package that owns the record: the account reaches it from the
// caller's own header, so an equality a caller could get wrong is one the
// boundary would be wrong with.
func (r RepoAuth) NamesAccount(account string) bool {
	return slices.Contains(r.Accounts, config.ReviewVariantAccount(account))
}

// AccountText is the record's accounts as one clause for a refusal, which names
// accounts and never a value (F27, C1.13). An empty list says so rather than
// printing nothing, because the sentence it lands in has to read as a reason: a
// repository whose record names no account is refused for exactly that.
func (r RepoAuth) AccountText() string {
	if len(r.Accounts) == 0 {
		return "no account"
	}
	return strings.Join(r.Accounts, ", ")
}
