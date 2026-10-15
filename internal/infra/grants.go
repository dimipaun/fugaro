package infra

import (
	"context"
	"fmt"

	secretmanager "google.golang.org/api/secretmanager/v1"
)

// GrantedSecretID reports which of ids currently grants member
// (a service account's own email, as RepoSpec.BuildServiceAccountEmail
// gives it) the secretAccessor role, and "" when none does.
//
// It exists for the CLI's own live call sites (a standalone fugaro image
// build, the GitHub App pre-checks): they resolve a logical name from live
// state, but the apply that last granted anything for this repository chose
// from an earlier state, and a grant fixed for one secret ID is not a grant
// for the other. The build account's own live grant is what they compare
// the resolution against -- never a deployed job, which the case they exist
// for (a repository's first build) has none of.
//
// ids are the candidates in the caller's own precedence order (the
// repository's own secret ID, then the project-wide one; "" for a name with
// no project candidate at all, which is skipped). A secret that does not
// exist is skipped rather than an error: a project candidate nobody has
// stored --project-wide yet has no container at all, and GetIamPolicy on it
// would 404 the same way Get does (discover.go's own loop reads the policy
// only after a successful Get, for the same reason).
//
// "" with no error is the pre-first-apply state: nothing has ever been
// granted for this repository, so there is nothing for a caller to disagree
// with. It is not, by itself, a drift case.
func GrantedSecretID(ctx context.Context, sm *secretmanager.Service, project, member string, ids ...string) (string, error) {
	if member == "" {
		return "", nil
	}
	tg := target{project: project}
	for _, id := range ids {
		if id == "" {
			continue
		}
		name := "projects/" + project + "/secrets/" + id
		if _, err := sm.Projects.Secrets.Get(name).Context(ctx).Do(); err != nil {
			switch {
			case absent(err, serviceSecretManager, tg):
				continue
			case err != nil:
				return "", fmt.Errorf("reading secret %s: %w", id, err)
			}
		}
		p, err := secretPolicy(ctx, sm, project, id)
		if err != nil {
			return "", err
		}
		if grantsAccessor(p, member) {
			return id, nil
		}
	}
	return "", nil
}

// secretPolicy reads secret id's live IAM policy, at version 3 so a
// conditional binding of the role is not silently dropped from the answer.
// It is the one GetIamPolicy read of a secret's policy, shared by
// DiscoverRepo's own accessor check and by GrantedSecretID: the caller has
// already established that the secret exists (DiscoverRepo through its own
// Get, GrantedSecretID through the same check), which is why the read
// itself is not guarded here.
func secretPolicy(ctx context.Context, sm *secretmanager.Service, project, id string) (*secretmanager.Policy, error) {
	p, err := sm.Projects.Secrets.GetIamPolicy("projects/" + project + "/secrets/" + id).
		OptionsRequestedPolicyVersion(3).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("reading the IAM policy of secret %s: %w", id, err)
	}
	return p, nil
}

// grantsAccessor reports whether policy grants member the secretAccessor
// role. A conditional binding of the role counts: it is still a grant a
// caller has to account for.
func grantsAccessor(p *secretmanager.Policy, member string) bool {
	if p == nil {
		return false
	}
	for _, b := range p.Bindings {
		if b.Role != roleSecretAccessor {
			continue
		}
		for _, m := range b.Members {
			if m == member {
				return true
			}
		}
	}
	return false
}
