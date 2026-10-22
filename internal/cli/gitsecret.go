package cli

import (
	"context"
	"fmt"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// gitSecretID is the secret ID a live call site (a standalone image build,
// a GitHub App pre-check) may name for spec's provider credential: the ID
// resolved from live state, checked against the grant the build account
// actually holds. It is empty when the provider has no credential at all
// (a provider neither Bitbucket nor GitHub).
func gitSecretID(ctx context.Context, lc *localcfg.Config, spec infra.RepoSpec, what string) (string, error) {
	logical, ok := infra.GitSecret(spec.Provider)
	if !ok {
		return "", nil
	}
	projectID, _ := infra.ProjectSecretCandidate(lc, logical, spec.Name)
	// The project is the local config's, the same one the client below is
	// built for: spec.GCPProject is that value (infra.Repo sets it from the
	// same config), and the two must not be able to disagree about which
	// project's secrets are being read.
	return resolveGitSecretID(ctx, lc, lc.GCPProject, spec.Name, logical, spec.Secrets[logical], projectID, buildAccountMember(spec.BuildServiceAccountEmail), what)
}

// buildAccountMember is the IAM member form of a repository's build service
// account email: the form a secret's policy bindings actually hold, which
// is what infra.GrantedSecretID compares against (the same
// "serviceAccount:" prefix discover.go's own accessor check builds).
func buildAccountMember(email string) string {
	if email == "" {
		return ""
	}
	return "serviceAccount:" + email
}

// appliedGitSecretID is the provider credential's ID spec carries after an
// apply already resolved and granted it (B1's init half): the project-wide
// copy when Readiness resolved the logical name to it, the repository's own
// otherwise. It reads that decision back out of the applied spec rather than
// resolving it live, because the grants the apply just made were fixed for
// this exact choice: re-deriving it could only disagree with them.
//
// Which ID that is is RepoSpec.MountSecretID's answer, not this function's:
// the mount the job got and the ID the build is submitted with must be the
// same choice, and the Terraform merge that decides the former is the map
// precedence MountSecretID spells.
func appliedGitSecretID(spec infra.RepoSpec) string {
	logical, ok := infra.GitSecret(spec.Provider)
	if !ok {
		return ""
	}
	return spec.MountSecretID(logical)
}

// resolveGitSecretID is gitSecretID's body over the names themselves, for
// the callers with no RepoSpec to hand: doctor's GitHub App check runs for
// every GitHub repository the local config records, and most of them have
// no checkout here.
//
// It resolves the logical name from live state (infra.ResolveSecretID: the
// repository's own copy wins when it has an enabled version, else the
// project-wide one) and compares that with the secret the build account is
// actually granted (infra.GrantedSecretID, B1).
//
// The comparison is against the account's own live grant, never a deployed
// job: the case this exists for -- a repository whose first image build is
// about to be submitted -- has no job to read at all. Three outcomes, and
// only the third refuses:
//   - the grant names the live-resolved ID: proceed.
//   - the grant names nothing at all (nothing has ever been applied for this
//     repository, the pre-first-apply state): proceed with the live-resolved
//     ID. A genuinely first build is not drift, and refusing here would block
//     it for no reason.
//   - the grant names a different, non-empty ID: refuse, naming both. The
//     secret's scope moved since the last apply (design §6 step 4a), so a
//     build's clone would read a secret that account may not read; the one
//     command that fixes it is the repository's own apply, which writes both
//     the mounts and the grants.
//
// what names the caller in the refusal, e.g. "the image build of acme/app".
func resolveGitSecretID(ctx context.Context, lc *localcfg.Config, gcpProject, repo, logical, repoID, projectID, buildSA, what string) (string, error) {
	sm, err := gcp.NewSecretManager(ctx, gcpOptions(lc))
	if err != nil {
		return "", remote(err)
	}
	id, _, err := infra.ResolveSecretID(ctx, sm, gcpProject, repoID, projectID)
	if err != nil {
		return "", remote(err)
	}
	granted, err := infra.GrantedSecretID(ctx, sm, gcpProject, buildSA, repoID, projectID)
	if err != nil {
		return "", remote(err)
	}
	if granted != "" && granted != id {
		return "", &ExitError{Code: ExitUserError, Err: &driftError{what: what, logical: logical, resolved: id, granted: granted, buildSA: buildSA, repo: repo}}
	}
	return id, nil
}

// driftError is resolveGitSecretID's one refusal: the secret ID live state
// resolves for a logical name is not the one the build account's own grant
// names, the case the apply that fixed that grant cannot have intended
// (design docs/design/project-secrets.md §6 step 4a).
type driftError struct {
	what, logical, resolved, granted, buildSA, repo string
}

func (e *driftError) Error() string {
	return fmt.Sprintf("%s reads %s from %s, but the build account %s is granted %s: the secret's scope changed since %s was last applied, and a build's clone would read a secret that account may not. Run %s update --repo %s in its checkout first, so the mounts and the grants agree again",
		e.what, e.logical, e.resolved, e.buildSA, e.granted, e.repo, selfCommand(), e.repo)
}

// short is Error() without the parts a doctor check's Problem cannot hold.
// That field goes through pluginwire.Printable, which cuts a string at
// MaxPrintable (200) runes: this message is built from two ~50-rune secret
// IDs, a service account email and a command path, so its tail -- the
// granted ID among it -- would be lost. The two IDs are the one thing a
// reader needs here; each caller names the apply that fixes it separately
// (doctor's own Fix, init's printed fix line).
func (e *driftError) short() string {
	return fmt.Sprintf("%s resolves to %s, but the build account is granted %s", e.logical, e.resolved, e.granted)
}
