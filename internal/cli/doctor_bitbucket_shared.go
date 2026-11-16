package cli

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/task"
)

// doctorBitbucketOwnTokenSharedJob is doctor's one warning about a Bitbucket
// repository's own copy (plan task T2.4, D466, D2302: a shared Bitbucket job
// holds no credential at all and the credentials service's proxy serves it
// from the project's token, paired by the profile).
//
// A repository is on a shared Bitbucket job exactly when the launcher puts it
// there, read the way the launcher reads it: the provider's default profile
// (runner.ProjectJobDefaultProfile, which launchProjectJobFor and the runner's
// candidate logic both call), a profile that passes projectpolicy.
// CheckSharedJobProfile — the rule a shared job's profile is deployed under —
// and a node that admits the repository to it. A workflow that names another
// profile is not this check's business: doctor resolves the provider default
// and says nothing about a selection only the repository's fugaro.yaml knows.
//
// It is deliberately not projectpolicy.SharedBitbucketProfile: that predicate
// is the per-repository job's (the provider AND the token mount, D473) and
// answers true for the very profile CheckSharedJobProfile refuses, so a check
// that asked it would warn about the one state whose own copy the job really
// mounts.
//
// What it warns about is a fact, never a defect in the configuration: the
// repository's own enabled copy is unreachable from its shared path and is
// still what its per-repository jobs mount. So it reads metadata only — one
// listing per admitted repository, by labels, never a value, as
// doctorProjectSecrets and doctorSecrets do — and an installation it cannot
// read (no Secret Manager endpoint, a listing that did not answer) says
// nothing rather than failing.
func doctorBitbucketOwnTokenSharedJob(ctx context.Context, lc *localcfg.Config, p *projectpolicy.Policy) []doctorCheck {
	repos := doctorBitbucketSharedRepos(lc, p)
	if len(repos) == 0 {
		return nil
	}
	if lc.Endpoints.NoAuth && lc.Endpoints.SecretManager == "" {
		// Fakes only, and no fake for Secret Manager: the same guard
		// doctorProjectSecrets makes.
		return nil
	}
	sm, err := gcp.NewSecrets(ctx, gcp.Options{GCPProject: lc.GCPProject, Region: lc.Region,
		Endpoints: gcp.Endpoints{SecretManager: lc.Endpoints.SecretManager, NoAuth: lc.Endpoints.NoAuth}})
	if err != nil {
		return nil
	}
	var out []doctorCheck
	for _, r := range repos {
		list, err := sm.List(ctx, map[string]string{
			gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: r.label, gcp.LabelSecret: config.BitbucketSecret})
		if err != nil {
			// A read that did not answer is not "no own copy": the check warns
			// about what it could see, and this repository said nothing.
			continue
		}
		if !projectSecretEnabled(list)[config.BitbucketSecret] {
			continue
		}
		out = append(out, doctorCheck{ID: "bitbucket-own-token-shared-job", Severity: "warning",
			Problem: fmt.Sprintf("%s's own %s copy holds an enabled version: its shared job holds no token and the proxy serves it from the project's token; its own copy applies only to its per-repository jobs",
				pluginwire.Printable(r.name), config.BitbucketSecret),
			Fix: "leave it as it is if this repository still has a per-repository job, which is what mounts it, or remove it with '" + selfCommand() + " secrets rm " + config.BitbucketSecret + "' if it has none"})
	}
	return out
}

// doctorBitbucketSharedRepo is one repository this machine's config names that
// the published node puts on a shared Bitbucket job: the name as the config
// spells it, for the warning, and the Secret Manager label of its own
// containers, for the listing.
type doctorBitbucketSharedRepo struct {
	name  string
	label string
}

// doctorBitbucketSharedRepos are the repositories admitted to a shared
// Bitbucket job, resolved the way a launch resolves them and in the config's
// own order (the names are sorted, so two runs print the same lines).
//
// The three questions are asked of one repository at a time and in this order,
// because each one makes the next meaningful: the provider's default profile
// (with no default the repository keeps its per-repository job), the shared-job
// rule, and only then the node's admission. A profile that fails the second is
// not a shared job at all — the deployer refuses it and doctor has its own
// lines for that — so its repositories are skipped rather than warned about.
func doctorBitbucketSharedRepos(lc *localcfg.Config, p *projectpolicy.Policy) []doctorBitbucketSharedRepo {
	if p == nil {
		return nil
	}
	var out []doctorBitbucketSharedRepo
	for _, name := range slices.Sorted(maps.Keys(lc.Repos)) {
		provider := lc.Repos[name].Provider
		if provider == "" {
			continue
		}
		canonical, err := projectpolicy.Canonical(provider, name)
		if err != nil {
			continue
		}
		profile := runner.ProjectJobDefaultProfile(p, provider)
		if profile == "" {
			continue
		}
		pr, err := p.ProfileFor(profile)
		if err != nil {
			continue
		}
		if err := projectpolicy.CheckSharedJobProfile("profiles."+profile, pr); err != nil {
			continue
		}
		admitted, err := p.Admits(profile, canonical)
		if err != nil || !admitted {
			continue
		}
		slug, err := task.Slug(provider, name)
		if err != nil {
			continue
		}
		label, err := gcp.RepoLabel(slug)
		if err != nil {
			continue
		}
		out = append(out, doctorBitbucketSharedRepo{name: name, label: label})
	}
	return out
}
