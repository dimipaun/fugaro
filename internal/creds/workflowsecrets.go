package creds

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// WorkflowSecrets is the record at creds/workflowsecrets/<slug>: what one
// repository's workflows may fetch from the credentials service, by name —
// each workflow's own secrets, the environment variable each becomes in the
// job, and the Secret Manager container its value is read from.
//
// # Why a record exists at all
//
// A workflow secret is stored once and mounted by the job (fugaro.yaml's
// `secrets:`), and the vault fetch of S3 changes where the value comes from,
// not which value it is (F33): the fetch reads the container the mount reads.
// Which container that is is Readiness's answer and not a name a reader can
// spell — the repository's own copy when it has an enabled version, the
// project-wide copy when the operator's local allow-list admits this
// repository to one (infra.ProjectSecretCandidate, D2, D1262) — and the
// service cannot read an operator's local config, so the answer has to be told
// to it (D2073). This node is that telling: `fugaro update` writes it from the
// mounts the repository's own run just resolved, and the service reads it on a
// workflow-secrets call (B3.2) and on a build call (B4.3).
//
// # No value, ever
//
// The container ID is a NAME. No field of this record is a credential's value
// and none may ever be (G18): the service reads the value out of the container
// with its own account, at call time, exactly as it reads the bundle's. The
// record is therefore safe to read, cache and log, and the closed shape below
// is what keeps it that way — a member that could carry a value is an edit a
// reviewer sees here and in the read, which refuses a member this release does
// not model.
//
// # Who may read it
//
// It lives under creds/, the subtree the database rules deny every run identity
// (O1, D136), and it is reached through a DB by two callers: the operator's CLI
// (internal/cli, which writes it) and the service (internal/credsvc, which
// serves from it). The record names the containers a repository's job may
// fetch, so a token that could read the node could enumerate what to ask for —
// which is the boundary internal/creds/boundary_test.go pins over the code
// half.
type WorkflowSecrets struct {
	// Version is the format's version. It is a member rather than a constant
	// because the writer and the reader are two binaries (the CLI writes, the
	// service reads), and a reader that meets a version it does not know
	// refuses the record rather than reading the members it recognises as the
	// whole of it.
	Version int `json:"version"`
	// Workflows maps a workflow's name to the secrets its job mounts, in env
	// order. A workflow with none is absent from the map, and a repository with
	// no workflow secrets at all is an empty map: the node is written on every
	// update, so a repository that dropped a secret gets a record that no
	// longer names it — an absent node would leave the previous update's answer
	// standing, which is a container the service would go on serving.
	Workflows map[string][]WorkflowSecret `json:"workflows"`
}

// WorkflowSecret is one secret of one workflow: the variable it becomes in the
// job, and the container the service reads its value from. It is the pair the
// job's own mount is built from (WorkflowSpec.SecretEnv with SecretIDs), which
// is why the build caller reads the same record (D2077 (a): a build secret's
// value comes from the service too, with the git credential, and the git
// credential is the build route's own item rather than an entry here).
type WorkflowSecret struct {
	// Env is the environment variable the value becomes in the job, and the
	// name a caller asks for.
	Env string `json:"env"`
	// ID is the container, in this installation's project: the short name
	// gcp.SecretID or gcp.ProjectSecretID answers, never a full resource path
	// and never a value.
	ID string `json:"id"`
}

const (
	// pathWorkflowSecrets is the family under creds/ the records live in, one
	// node per repository slug.
	pathWorkflowSecrets = "creds/workflowsecrets"

	// WorkflowSecretsVersion is the version this release writes and reads.
	WorkflowSecretsVersion = 1
)

// PathWorkflowSecrets is one repository's record. It panics on an empty slug,
// as every path builder in this package does: a slug reaches it from a task or
// a spec, never from a request.
func PathWorkflowSecrets(slug string) string {
	return pathWorkflowSecrets + "/" + seg("slug", slug)
}

// The record's refusals, as sentinels so a caller can answer without reading a
// message. Neither carries a value or a caller's own text.
var (
	// ErrNoWorkflowSecrets is a slug that names no repository: no path is built
	// from it (an empty segment would collapse onto another node).
	ErrNoWorkflowSecrets = errors.New("creds: the workflow secrets record names no repository")
	// ErrWorkflowSecretsMalformed is a record that could not be written as it
	// stands: a workflow with no name, a secret with no variable or no
	// container, or one variable named twice in one workflow — an answer no
	// reader could decide from.
	ErrWorkflowSecretsMalformed = errors.New("creds: a workflow secrets record must name one container per variable, once each")
	// ErrWorkflowSecretsVersion is a record written by a release this one does
	// not know. It is refused rather than half read: the members this release
	// recognises are not the whole of a document it did not write.
	ErrWorkflowSecretsVersion = errors.New("creds: the workflow secrets record was written by another version of fugaro")
)

// ReadWorkflowSecrets reads one repository's record. found=false is a
// repository no update has recorded — an installation that has not run the
// release that writes these, or one whose repository has no workflow secrets
// and whose run never reached the write — and it is not an error: the caller
// decides what an absent record means, and for the service that is "serve
// nothing" (fail closed, G19).
//
// A node that is present but unreadable IS an error, and so is a version this
// release does not know: a record half understood is one that could name a
// container the repository never mounted.
func ReadWorkflowSecrets(ctx context.Context, db DB, slug string) (WorkflowSecrets, bool, error) {
	if slug == "" {
		return WorkflowSecrets{}, false, ErrNoWorkflowSecrets
	}
	path := PathWorkflowSecrets(slug)
	var raw json.RawMessage
	_, found, err := db.GetETag(ctx, path, &raw)
	if err != nil {
		return WorkflowSecrets{}, false, fmt.Errorf("creds: reading %s: %w", path, err)
	}
	if !found || len(raw) == 0 {
		return WorkflowSecrets{}, false, nil
	}
	rec, err := decodeStrict[WorkflowSecrets](raw)
	if err != nil {
		return WorkflowSecrets{}, false, fmt.Errorf("creds: reading %s: %w", path, err)
	}
	if rec.Version != WorkflowSecretsVersion {
		return WorkflowSecrets{}, false, fmt.Errorf("creds: reading %s: %w (version %d)", path, ErrWorkflowSecretsVersion, rec.Version)
	}
	return rec, true, nil
}

// PutWorkflowSecrets writes one repository's record whole, replacing whatever
// the node held. It is a whole replacement and not a merge: the record says
// what the repository's workflows mount NOW, so a row of an earlier update's
// that this run no longer mounts has to go — a merge would leave the service
// serving a container the job stopped mounting, which is exactly the reach
// G19 bounds.
//
// The write is conditional on the value read (cas), so a concurrent update of
// the same repository leaves one of the two answers rather than a torn one,
// and a node this release cannot read — one a newer fugaro wrote — is refused
// instead of clobbered: fail closed at the writer, where the mistake would
// otherwise become a record nothing can be decided from.
//
// The entries are put in env order here rather than left to the caller, so the
// node is byte-identical for the same mounts however the config listed them,
// and the version is the writer's for the same reason: the format is this
// release's, and a caller does not get to name another one.
func PutWorkflowSecrets(ctx context.Context, db DB, slug string, rec WorkflowSecrets) error {
	if slug == "" {
		return ErrNoWorkflowSecrets
	}
	next := WorkflowSecrets{Version: WorkflowSecretsVersion, Workflows: make(map[string][]WorkflowSecret, len(rec.Workflows))}
	for name, secrets := range rec.Workflows {
		if name == "" {
			return ErrWorkflowSecretsMalformed
		}
		rows := slices.Clone(secrets)
		slices.SortFunc(rows, func(a, b WorkflowSecret) int { return strings.Compare(a.Env, b.Env) })
		for i, s := range rows {
			if s.Env == "" || s.ID == "" {
				return ErrWorkflowSecretsMalformed
			}
			if i > 0 && rows[i-1].Env == s.Env {
				return ErrWorkflowSecretsMalformed
			}
		}
		if len(rows) > 0 {
			next.Workflows[name] = rows
		}
	}
	path := PathWorkflowSecrets(slug)
	_, err := cas(ctx, db, path, DefaultAttempts, func(WorkflowSecrets, bool) (WorkflowSecrets, error) {
		return next, nil
	})
	if err != nil {
		return fmt.Errorf("creds: writing %s: %w", path, err)
	}
	return nil
}
