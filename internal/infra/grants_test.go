package infra

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

// accessor is a policy granting member the one role GrantedSecretID looks
// for, the role every Fugaro grant uses (gcp.SecretAccessorRole).
func accessor(member string) []gcpfake.Binding {
	return []gcpfake.Binding{{Role: gcp.SecretAccessorRole, Members: []string{member}}}
}

// B1: which of the candidates the build account's own live grant names.
// The repository copy's grant is the answer when both grant it: it is the
// caller's own precedence order, the same one ResolveSecretID uses.
func TestGrantedSecretIDFindsRepoGrant(t *testing.T) {
	f := newCloud(t)
	f.sm.Seed("repo-id", nil, []byte("v"))
	f.sm.Seed("project-id", nil, []byte("v"))
	f.sm.SetPolicy("repo-id", accessor("serviceAccount:build@proj-1234.iam.gserviceaccount.com"))
	f.sm.SetPolicy("project-id", accessor("serviceAccount:build@proj-1234.iam.gserviceaccount.com"))
	id, err := GrantedSecretID(t.Context(), resolveSM(t, f), "proj-1234", "serviceAccount:build@proj-1234.iam.gserviceaccount.com", "repo-id", "project-id")
	if err != nil || id != "repo-id" {
		t.Fatalf("id %q, err %v: want the repository's own copy", id, err)
	}
}

func TestGrantedSecretIDFindsProjectGrant(t *testing.T) {
	f := newCloud(t)
	f.sm.Seed("repo-id", nil, []byte("v"))
	f.sm.Seed("project-id", nil, []byte("v"))
	f.sm.SetPolicy("repo-id", accessor("serviceAccount:someone-else@proj-1234.iam.gserviceaccount.com"))
	f.sm.SetPolicy("project-id", accessor("serviceAccount:build@proj-1234.iam.gserviceaccount.com"))
	id, err := GrantedSecretID(t.Context(), resolveSM(t, f), "proj-1234", "serviceAccount:build@proj-1234.iam.gserviceaccount.com", "repo-id", "project-id")
	if err != nil || id != "project-id" {
		t.Fatalf("id %q, err %v: want the project-wide copy", id, err)
	}
}

// The pre-first-apply state: "" with no error, which the caller treats as
// agreement rather than a refusal (a repository's genuinely first build).
func TestGrantedSecretIDNeitherGranted(t *testing.T) {
	f := newCloud(t)
	f.sm.Seed("repo-id", nil, []byte("v"))
	f.sm.Seed("project-id", nil, []byte("v"))
	id, err := GrantedSecretID(t.Context(), resolveSM(t, f), "proj-1234", "serviceAccount:build@proj-1234.iam.gserviceaccount.com", "repo-id", "project-id")
	if err != nil || id != "" {
		t.Fatalf("id %q, err %v: want \"\" and no error", id, err)
	}
}

// A project candidate nobody has stored --project-wide yet has no container
// at all: its Get 404s, and the read is skipped rather than erroring, the
// identical order DiscoverRepo's own secretPolicies population uses.
func TestGrantedSecretIDSkipsMissingCandidate(t *testing.T) {
	f := newCloud(t)
	f.sm.Seed("repo-id", nil, []byte("v"))
	f.sm.SetPolicy("repo-id", accessor("serviceAccount:build@proj-1234.iam.gserviceaccount.com"))
	id, err := GrantedSecretID(t.Context(), resolveSM(t, f), "proj-1234", "serviceAccount:build@proj-1234.iam.gserviceaccount.com", "repo-id", "project-id")
	if err != nil || id != "repo-id" {
		t.Fatalf("id %q, err %v: a candidate with no container is skipped, not an error", id, err)
	}
}

// A conditional binding of the role still counts: the policy is read at
// version 3 so it is not silently dropped from the answer.
func TestGrantedSecretIDCountsConditionalBinding(t *testing.T) {
	f := newCloud(t)
	f.sm.Seed("repo-id", nil, []byte("v"))
	f.sm.SetPolicy("repo-id", []gcpfake.Binding{{Role: gcp.SecretAccessorRole,
		Members: []string{"serviceAccount:build@proj-1234.iam.gserviceaccount.com"},
		Condition: &gcpfake.IAMCondition{Expression: "request.time < timestamp('2030-01-01T00:00:00Z')"}}})
	id, err := GrantedSecretID(t.Context(), resolveSM(t, f), "proj-1234", "serviceAccount:build@proj-1234.iam.gserviceaccount.com", "repo-id", "")
	if err != nil || id != "repo-id" {
		t.Fatalf("id %q, err %v: a conditional binding of the role is still a grant", id, err)
	}
}
