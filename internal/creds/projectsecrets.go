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

// ProjectSecrets is the record at creds/projectsecrets: which repositories may
// fetch a project-shared workflow secret from the credentials service, by name
// — the allow-list D3328 makes the owner's own decision and the service checks
// against a run's verified repository (D3333).
//
// # Why a record exists at all
//
// Under D2313 the project copy is the only one a run reads, and a run names the
// logical secret of its own fugaro.yaml when it fetches (C1's version-2
// request). The service cannot read an operator's local config, so what a name
// reaches has to be told to it: `fugaro secrets allow` and `deny` write this
// node, and the service reads it on a workflow-secrets call (IS1). The
// operator-local `workflow_secrets` block keeps its own entry — version 1 and
// the mounts still read it — but the record is the list a version-2 fetch is
// admitted by, and it is the only member that can say "every repository of the
// project, whatever its owner" (all_repos), which the local block's
// owner-literal rule refuses (O2, internal/config/project_secrets.go).
//
// # No value, ever
//
// No field of this record is a credential's value and none may ever be (G18):
// every member is a name or a repository pattern. It is therefore safe to read,
// cache and log, and the closed shape below — a version, the names, each one's
// patterns and its all_repos switch — is what keeps it that way. A member that
// could carry a value is an edit a reviewer sees here and in the read, which
// refuses a member this release does not model.
//
// # Who may read it
//
// It lives under creds/, the subtree the database rules deny every run identity
// (O1, D136). It is reached through a DB by two callers: the operator's CLI
// (internal/cli, which writes it) and the service (internal/credsvc, which
// serves from it).
type ProjectSecrets struct {
	// Version is the format's version. It is a member rather than a constant
	// because the writer and the reader are two binaries (the CLI writes, the
	// service reads), and a reader that meets a version it does not know refuses
	// the record rather than reading the members it recognises as the whole of
	// it.
	Version int `json:"version"`
	// Secrets maps a workflow-secret name to what may fetch it. A name no
	// repository may fetch is absent from the map, and a name whose list a
	// `deny` emptied keeps its entry with an empty list rather than losing it:
	// update seeds a name only when the record holds no entry for it, so an
	// absent entry is a name `fugaro update` would re-seed from the operator's
	// local block and a deny would be undone (F23).
	Secrets map[string]ProjectSecretRule `json:"secrets"`
}

// ProjectSecretRule is what one workflow-secret name's entry says: the
// repository patterns that may fetch it, and the switch that admits every
// repository of the project instead.
type ProjectSecretRule struct {
	// Repos are the repository patterns allowed to fetch the value, in the
	// spelling the local block's allow_data_to uses: owner/name, with "*" inside
	// a segment and never across "/", and a literal owner (O2 keeps the local
	// block's rule and stores all-repos as its own member here instead). A
	// pattern means every repository it matches, now and later.
	Repos []string `json:"repos"`
	// AllRepos admits every repository enrolled in the project, whatever its
	// owner — the reach `fugaro secrets allow NAME --all-repos` writes and the
	// one the local block's owner-literal rule is never meant to make (O2,
	// answered 2026-11-23: every repository, any owner). It is a member of its
	// own rather than a "*" in Repos so the two reaches stay tellable apart:
	// one is a list the owner wrote, the other is the whole project.
	AllRepos bool `json:"all_repos"`
}

// pathProjectSecrets is the node the record lives at: one document for the
// installation, not one per repository (the per-repository record is
// pathWorkflowSecrets, and version 1 reads that one until the follow-up).
const pathProjectSecrets = "creds/projectsecrets"

// ProjectSecretsVersion is the version this release writes and reads.
const ProjectSecretsVersion = 1

// PathProjectSecrets is the record's node. It takes no argument — the record is
// the installation's own, one document for every name — and it is a function
// rather than a constant so a reader gets the path the same way it gets every
// other path in this package.
func PathProjectSecrets() string { return pathProjectSecrets }

// The record's refusals, as sentinels so a caller can answer without reading a
// message. Neither carries a value or a caller's own text.
var (
	// ErrProjectSecretsMalformed is a record that could not be written as it
	// stands: a key that is not a workflow-secret name, or an empty repository
	// pattern. An entry naming no repository at all is NOT malformed — that is
	// exactly what a `deny` leaves (F23), and the entry has to stay for update
	// not to re-seed it.
	ErrProjectSecretsMalformed = errors.New("creds: a project secrets record must name only workflow-secret names, with no empty repository pattern")
	// ErrProjectSecretsVersion is a record written by a release this one does
	// not know. It is refused rather than half read: the members this release
	// recognises are not the whole of a document it did not write.
	ErrProjectSecretsVersion = errors.New("creds: the project secrets record was written by another version of fugaro")
)

// ReadProjectSecrets reads the project record. found=false is an installation
// no `secrets allow` has written yet — the release behind, or one whose owner
// has allow-listed nothing — and it is not an error: the caller decides what an
// absent record means, and for the service that is "serve nothing" (fail
// closed, G19).
//
// A node that is present but unreadable IS an error, and so is a version this
// release does not know: a record half understood is one that could admit a
// repository the owner never listed.
func ReadProjectSecrets(ctx context.Context, db DB) (ProjectSecrets, bool, error) {
	var raw json.RawMessage
	_, found, err := db.GetETag(ctx, pathProjectSecrets, &raw)
	if err != nil {
		return ProjectSecrets{}, false, fmt.Errorf("creds: reading %s: %w", pathProjectSecrets, err)
	}
	if !found || len(raw) == 0 {
		return ProjectSecrets{}, false, nil
	}
	rec, err := decodeStrict[ProjectSecrets](raw)
	if err != nil {
		return ProjectSecrets{}, false, fmt.Errorf("creds: reading %s: %w", pathProjectSecrets, err)
	}
	if rec.Version != ProjectSecretsVersion {
		return ProjectSecrets{}, false, fmt.Errorf("creds: reading %s: %w (version %d)", pathProjectSecrets, ErrProjectSecretsVersion, rec.Version)
	}
	if err := validateProjectSecrets(rec); err != nil {
		return ProjectSecrets{}, false, fmt.Errorf("creds: reading %s: %w", pathProjectSecrets, err)
	}
	return rec, true, nil
}

// WriteProjectSecrets writes the record through the compare-and-swap every
// writer of this database makes: f is handed the record as it stands (the zero
// value when the node is absent) and what it answers is written under the ETag
// that was read, so a lost race re-reads and calls f again rather than
// clobbering a name another operator wrote between the two (F17's rule, and
// `secrets allow` on one name must not drop another's).
//
// f is called again after a conflict, so it must answer only from its
// arguments. The version is the writer's: the format is this release's, and a
// caller does not get to name another one. A node whose version member is not
// this release's is refused instead of clobbered (fail closed at the writer,
// where rewriting it as version 1 would drop a newer release's members with no
// error anywhere).
func WriteProjectSecrets(ctx context.Context, db DB, f func(cur ProjectSecrets, found bool) (ProjectSecrets, error)) error {
	_, err := cas(ctx, db, pathProjectSecrets, DefaultAttempts, func(cur ProjectSecrets, found bool) (ProjectSecrets, error) {
		if found && cur.Version != ProjectSecretsVersion {
			return ProjectSecrets{}, fmt.Errorf("%w (version %d)", ErrProjectSecretsVersion, cur.Version)
		}
		next, err := f(cur, found)
		if err != nil {
			return ProjectSecrets{}, err
		}
		return normalizeProjectSecrets(next)
	})
	if err != nil {
		return fmt.Errorf("creds: writing %s: %w", pathProjectSecrets, err)
	}
	return nil
}

// Rule answers name's entry as it stands. ok is false when the record holds no
// entry for the name — the state update seeds from the local block and the one
// a `deny` never leaves (it leaves an entry with an empty list, F23).
func (rec ProjectSecrets) Rule(name string) (ProjectSecretRule, bool) {
	rule, ok := rec.Secrets[name]
	return rule, ok
}

// WithRule returns rec with name's entry replaced by rule. A name the record
// does not hold is added; an entry is never removed here, because a removal is
// the state update re-seeds from and a deny leaves an empty one instead (F23).
// The map is copied, so a caller's record is not mutated by the answer.
func (rec ProjectSecrets) WithRule(name string, rule ProjectSecretRule) ProjectSecrets {
	next := rec
	next.Secrets = make(map[string]ProjectSecretRule, len(rec.Secrets)+1)
	for k, v := range rec.Secrets {
		next.Secrets[k] = v
	}
	next.Secrets[name] = rule
	return next
}

// Allows reports whether repo (owner/name) may fetch the project-shared
// workflow secret name: the record must hold an entry for the name, and that
// entry must either admit every repository (AllRepos) or list a pattern repo
// matches. The matching is the local block's own rule, reused rather than
// restated — a "*" inside a segment and never across "/", and a literal owner —
// so the record and the block can never come to answer differently about what
// "myworkspace/*" reaches (O2).
func (rec ProjectSecrets) Allows(name, repo string) bool {
	rule, ok := rec.Secrets[name]
	if !ok {
		return false
	}
	if rule.AllRepos {
		return true
	}
	return config.ProjectSecret{AllowDataTo: rule.Repos}.AllowsData(repo)
}

// normalizeProjectSecrets stamps the version this release writes, sorts and
// de-duplicates each rule's patterns so the node is byte-identical for the same
// list however the caller spelled it, and refuses a record no reader could
// decide from. The version is the writer's for the same reason PutWorkflowSecrets'
// is: a caller does not get to name another one.
func normalizeProjectSecrets(rec ProjectSecrets) (ProjectSecrets, error) {
	next := ProjectSecrets{Version: ProjectSecretsVersion, Secrets: make(map[string]ProjectSecretRule, len(rec.Secrets))}
	for name, rule := range rec.Secrets {
		repos := slices.Clone(rule.Repos)
		slices.Sort(repos)
		repos = slices.Compact(repos)
		entry := ProjectSecretRule{Repos: repos, AllRepos: rule.AllRepos}
		if err := validateProjectSecretRule(name, entry); err != nil {
			return ProjectSecrets{}, err
		}
		next.Secrets[name] = entry
	}
	return next, nil
}

// validateProjectSecrets refuses a record this release could not decide from,
// whole: every name a workflow-secret name, every rule naming something.
func validateProjectSecrets(rec ProjectSecrets) error {
	for name, rule := range rec.Secrets {
		if err := validateProjectSecretRule(name, rule); err != nil {
			return err
		}
	}
	return nil
}

// validateProjectSecretRule is the one rule's check, shared by the reader and
// the writer so the two ends cannot part company about what an entry is. An
// entry naming no repository is legal and is left alone: that is the entry a
// `deny` that emptied the list leaves (F23).
func validateProjectSecretRule(name string, rule ProjectSecretRule) error {
	if !config.SecretNameRE.MatchString(name) {
		return fmt.Errorf("%w: %q is not lower-case letters, digits and dashes", ErrProjectSecretsMalformed, name)
	}
	for _, r := range rule.Repos {
		if strings.TrimSpace(r) == "" {
			return fmt.Errorf("%w: %q names an empty repository pattern", ErrProjectSecretsMalformed, name)
		}
	}
	return nil
}
