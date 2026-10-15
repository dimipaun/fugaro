package infra

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	secretmanager "google.golang.org/api/secretmanager/v1"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/runner"
)

// MissingKind says what a readiness gate is waiting for.
type MissingKind string

// The kinds of gate.
const (
	// MissingSecret is a secret the job mounts that has no version.
	MissingSecret MissingKind = "secret"
	// MissingImage is the job's image, not built yet.
	MissingImage MissingKind = "image"
)

// Missing is something a workflow's job needs that doesn't exist yet.
type Missing struct {
	Workflow string
	Kind     MissingKind
	Reason   string
	// Deployed means the job exists and stays deployed anyway, so this is
	// a warning; otherwise the job isn't deployed until it is fixed.
	Deployed bool
	// Check means the job is the daily image check's, not Workflow's.
	Check bool
}

func (m Missing) String() string {
	if m.Check {
		if m.Deployed {
			return "the daily image check (its job exists and stays deployed): " + m.Reason
		}
		return "the daily image check (its job, invoker grant and schedule are not deployed yet): " + m.Reason
	}
	if m.Deployed {
		return fmt.Sprintf("workflow %s (its job exists and stays deployed): %s", m.Workflow, m.Reason)
	}
	return fmt.Sprintf("workflow %s (its job is not deployed yet): %s", m.Workflow, m.Reason)
}

// errFound stops a listing at its first match.
var errFound = errors.New("found")

// OptionalSecret reports whether a job may go without this logical secret.
// Only one may: the Anthropic API key, mounted whenever the secret has a
// version, because a run whose models all route through providers needs none
// (design docs/design/project-centric.md §6.1). Everything else a job mounts
// is required for the job to work at all.
func OptionalSecret(logical string) bool { return logical == config.AnthropicSecret }

// dropMount removes a logical secret from a job's mounts: the variable it
// would become is deleted, so no secret_key_ref points at a secret with no
// version (Cloud Run checks a job's secrets when it creates it), and the
// runner's redaction list is rebuilt from the variables that are left.
func dropMount(ws WorkflowSpec, logical string) {
	for env, l := range ws.SecretEnv {
		if l == logical {
			delete(ws.SecretEnv, env)
		}
	}
	delete(ws.SecretIDs, logical)
	if ws.Env != nil {
		ws.Env[runner.SecretEnvsVar] = strings.Join(slices.Sorted(maps.Keys(ws.SecretEnv)), ",")
	}
}

// Readiness decides, from what exists, what the plan may deploy. It
// returns a deep copy of spec with the gates applied (spec itself is not
// changed, and shares no map or slice with the result) and what is still
// missing:
//   - A workflow's job is deployed when it already exists (a job is never
//     removed), or when its image's latest tag exists in the repository's
//     registry and every secret it mounts has an enabled version.
//   - An existing job keeps its current image until the spec's image has
//     a latest tag, so no apply points a job at a missing image. A job
//     with no image and an unbuilt spec image is refused (a user error).
//   - A job account keeps the display name discovery found (ex).
//   - The daily check's job, its invoker grant and its Scheduler job are
//     deployed when the check job already exists, or when every secret it
//     mounts (the provider credential) has an enabled version: Cloud Run
//     checks a job's secrets when it creates it.
//   - The daily check's schedule stays paused until every workflow it
//     covers has a build record, so no unattended build starts for an
//     image nobody has built on purpose.
func Readiness(ctx context.Context, c *Clients, spec RepoSpec, ex Existing) (RepoSpec, []Missing, error) {
	out := cloneRepoSpec(spec)
	tg := target{project: spec.GCPProject, number: ex.ProjectNumber}
	// A secret a repository's own Terraform module once created can stop
	// being computed by the current config (a provider that was eligible
	// everywhere under §3a, now excluded by a newly added list). Its
	// container must stay in Terraform's for_each -- prevent_destroy would
	// otherwise fail the next plan outright (P2, extended; N4) -- so every
	// correctly-labelled one discovery found is unioned back in. Only the
	// mount is dropped, by SecretMounts's own, config-driven output.
	maps.Copy(out.Secrets, ex.ExtraSecrets)
	versions := map[string]bool{} // secret ID → has an enabled version
	var missing []Missing
	hasVersionID := func(id string) (bool, error) {
		ok, seen := versions[id]
		if !seen {
			var err error
			if ok, err = hasEnabledVersion(ctx, c.Secrets, tg, id); err != nil {
				return false, err
			}
			versions[id] = ok
		}
		return ok, nil
	}
	hasVersion := func(logical string) (bool, error) { return hasVersionID(spec.Secrets[logical]) }
	// resolveProject is Readiness's own repo-wins-else-project precedence
	// (design docs/design/project-secrets.md §5), through the one helper
	// that decides it (resolveSecretID, ResolveSecretID's own body). It is
	// only ever called for a logical name whose repository copy has no
	// enabled version, so resolved is true exactly when the project copy is
	// the one that resolves; fix names the command that would change that.
	resolveProject := func(logical string) (resolved bool, fix string, err error) {
		fix = fmt.Sprintf("fugaro secrets set %s --repo %s", logical, spec.Name)
		cand, isCandidate := spec.ProjectSecretCandidates[logical]
		if !isCandidate {
			return false, fix, nil
		}
		fix = fmt.Sprintf("fugaro secrets set %s --project-wide", logical)
		if !ex.ProjectSecretsOK[logical] {
			return false, fix, nil
		}
		id, ok, err := resolveSecretID(hasVersionID, spec.Secrets[logical], cand)
		if err != nil {
			return false, fix, err
		}
		if !ok || id != cand {
			return false, fix, nil
		}
		if out.ProjectSecrets == nil {
			out.ProjectSecrets = map[string]string{}
		}
		out.ProjectSecrets[logical] = cand
		return true, fix, nil
	}
	for _, name := range slices.Sorted(maps.Keys(out.Workflows)) {
		ws := out.Workflows[name]
		if dn, ok := ex.DisplayNames[name]; ok {
			ws.ServiceAccount.DisplayName = dn
		}
		type gate struct {
			kind   MissingKind
			reason string
		}
		var reasons []gate
		var unresolved []string
		for _, logical := range uniqueSecrets(ws) {
			ok, err := hasVersion(logical)
			if err != nil {
				return RepoSpec{}, nil, err
			}
			if ok {
				continue
			}
			if OptionalSecret(logical) {
				// Mounted whenever the secret has a version, and a fact
				// about data rather than about policy, so flipping a
				// routing setting never needs Terraform (design
				// project-centric §6.1). With no version anywhere the
				// mount is dropped and the job deploys anyway: a run whose
				// models all route through providers needs no such key,
				// and a call that needs it is refused by the gateway by
				// name, not blocked here.
				dropMount(ws, logical)
				continue
			}
			resolved, fix, err := resolveProject(logical)
			if err != nil {
				return RepoSpec{}, nil, err
			}
			if resolved {
				continue
			}
			unresolved = append(unresolved, logical)
			reasons = append(reasons, gate{MissingSecret, fmt.Sprintf("secret %s has no version: %s", logical, fix)})
		}
		current, exists := ex.Jobs[name]
		// P7a: an existing job must never carry a secret_env entry with no
		// version anywhere. Omitting it (rather than refusing the update)
		// keeps the job's other properties updating; DeployJob is never set
		// false for a job that already exists, since job.tf's count would
		// destroy it outright. The Missing above is still reported.
		if exists && len(unresolved) > 0 {
			for _, logical := range unresolved {
				dropMount(ws, logical)
			}
		}
		var built bool
		if ws.Checkout == config.CheckoutClone {
			// A clone workflow's image is the installation's base
			// (base_images.base), never a tag in the repository's own
			// registry: there is nothing for hasLatest to check, and the
			// job is ready as soon as the local config names one.
			built = ws.Image != ""
			if !built {
				reasons = append(reasons, gate{MissingImage, fmt.Sprintf("the local config has no base_images.%s, which workflow %s's checkout: clone job runs: fugaro init --base %s", config.BaseKind, ws.Name, config.BaseKind)})
			}
		} else {
			b, err := hasLatest(ctx, c, tg, spec, ws.Image)
			if err != nil {
				return RepoSpec{}, nil, err
			}
			built = b
			if !built {
				// --workflow takes a fugaro.yaml workflow name, never a
				// gcp.WorkflowVariant key (several of out.Workflows' keys can
				// share one ws.Name and its one shared image).
				reasons = append(reasons, gate{MissingImage, fmt.Sprintf("image not built yet: fugaro image build --repo %s --workflow %s", spec.Name, ws.Name)})
			}
		}
		switch {
		case exists:
			ws.DeployJob = true
			if !built {
				if current == "" {
					// Nothing live to keep, and the spec's image doesn't
					// exist: either would leave the job without an image. A
					// clone workflow's ws.Image is empty in this case (there
					// is no base_images.base to name), so the message names
					// the missing config key instead of an empty image.
					// --workflow takes a fugaro.yaml workflow name (ws.Name),
					// never a gcp.WorkflowVariant key.
					what := fmt.Sprintf("%s is not built yet", ws.Image)
					fix := fmt.Sprintf("build it (fugaro image build --repo %s --workflow %s)", spec.Name, ws.Name)
					if ws.Checkout == config.CheckoutClone {
						what = fmt.Sprintf("the local config has no base_images.%s", config.BaseKind)
						fix = fmt.Sprintf("set it (fugaro init --base %s)", config.BaseKind)
					}
					return RepoSpec{}, nil, userErr("Cloud Run job %s exists but has no container image, and %s; %s or fix the job, then run fugaro init --repo again",
						ws.Job, what, fix)
				}
				ws.Image = current
			}
		default:
			ws.DeployJob = len(reasons) == 0
		}
		for _, r := range reasons {
			missing = append(missing, Missing{Workflow: name, Kind: r.kind, Reason: r.reason, Deployed: exists})
		}
		out.Workflows[name] = ws
	}
	if spec.Check != nil {
		ready := true
		var unresolved []string
		// The provider credential's resolved ID, carried by the check job's
		// own spec so the rebuild it submits names the secret it reads
		// (CheckJobSpec.GitSecretID, design §7): derived here from the very
		// decision this loop just made, not resolved a second time.
		gitLogical, _ := GitSecret(spec.Provider)
		gitSecretID := ""
		for _, logical := range slices.Compact(slices.Sorted(maps.Values(out.Check.SecretEnv))) {
			ok, err := hasVersion(logical)
			if err != nil {
				return RepoSpec{}, nil, err
			}
			if ok {
				if logical == gitLogical {
					gitSecretID = spec.Secrets[logical]
				}
				continue
			}
			// B4: the check job mounts a subset of the same logical names,
			// and reads the same repo-wide local.secrets Terraform local, so
			// it resolves precedence identically.
			resolved, fix, err := resolveProject(logical)
			if err != nil {
				return RepoSpec{}, nil, err
			}
			if resolved {
				if logical == gitLogical {
					gitSecretID = spec.ProjectSecretCandidates[logical]
				}
				continue
			}
			unresolved = append(unresolved, logical)
			ready = false
			missing = append(missing, Missing{Check: true, Kind: MissingSecret, Deployed: ex.CheckJob,
				Reason: fmt.Sprintf("secret %s has no version: %s", logical, fix)})
		}
		if err := setCheckGitSecretID(out.Check, gitSecretID); err != nil {
			return RepoSpec{}, nil, err
		}
		// P7a for the check job: it runs as the build account and its image
		// is always the base image, so an unresolvable entry left in place
		// would point secret_key_ref at a secret with zero versions.
		if ex.CheckJob && len(unresolved) > 0 {
			for _, logical := range unresolved {
				for env, l := range out.Check.SecretEnv {
					if l == logical {
						delete(out.Check.SecretEnv, env)
					}
				}
			}
			if out.Check.Env != nil {
				out.Check.Env[runner.SecretEnvsVar] = strings.Join(slices.Sorted(maps.Keys(out.Check.SecretEnv)), ",")
			}
		}
		out.Check.DeployJob = ready || ex.CheckJob
		// Paused unless there is at least one workflow and each has a
		// record, so an empty list fails safe. The spec's runs bucket is
		// bucketName(lc), the bucket builds record in (lc.RecordBucketURL).
		records := 0
		for _, name := range out.Check.Workflows {
			ok, err := hasRecord(ctx, c, tg, spec.Installation.RunsBucket, imagecheck.RecordKey(spec.Slug, name))
			if err != nil {
				return RepoSpec{}, nil, err
			}
			if ok {
				records++
			}
		}
		out.Check.Paused = records == 0 || records < len(out.Check.Workflows)
	}
	return out, missing, nil
}

// ResolveSecretID is the one place Readiness's own repo-wins-else-project
// precedence lives, extracted so the CLI's own live call sites (image
// builds, the GitHub App pre-checks) can resolve a single logical name
// without a full Readiness pass. repoID is always checked first; projectID
// is checked only if repoID has no enabled version, and projectID == "" means
// no candidate exists for this repository (not eligible at all), in which
// case repoID is returned regardless of its own version state -- byte-for-byte
// today's unconditional repo-only behavior for a non-eligible name. hasVersion
// is the chosen ID's own version state, so a caller can report a
// Missing-style error the same way Readiness already does. project is the
// GCP project both IDs live in.
func ResolveSecretID(ctx context.Context, sm *secretmanager.Service, project, repoID, projectID string) (id string, hasVersion bool, err error) {
	tg := target{project: project}
	has := func(id string) (bool, error) { return hasEnabledVersion(ctx, sm, tg, id) }
	return resolveSecretID(has, repoID, projectID)
}

// resolveSecretID is ResolveSecretID over a version lookup the caller
// controls: Readiness passes its own memoized one, so the precedence is
// still decided in one place while a full pass still asks about each
// secret at most once.
func resolveSecretID(hasVersion func(string) (bool, error), repoID, projectID string) (string, bool, error) {
	ok, err := hasVersion(repoID)
	if err != nil {
		return repoID, false, err
	}
	if ok || projectID == "" || projectID == repoID {
		return repoID, ok, nil
	}
	ok, err = hasVersion(projectID)
	if err != nil {
		return repoID, false, err
	}
	if ok {
		return projectID, true, nil
	}
	return repoID, false, nil
}

// setCheckGitSecretID rewrites the check job's own spec (CheckSpecEnv) with
// the secret ID Readiness resolved for the provider credential. It is the
// one place Readiness touches an env var's value rather than only which
// secret ID a job mounts, and it is why the job's own rebuild cannot name a
// different copy than the job's clone reads: the ID travels with the
// deployed job.
//
// An absent or unparsable spec is left exactly as it is: infra.Repo always
// writes one, and refusing a spec this fugaro cannot read is checkjob.go's
// own, separate gate. id == "" (nothing resolved anywhere) leaves the field
// empty, which the in-job rebuild reads as "fall back to the repository's
// own ID" -- the pre-feature behavior, and never a silently missing mount.
func setCheckGitSecretID(check *CheckSpec, id string) error {
	if id == "" || check.Env == nil {
		return nil
	}
	raw, ok := check.Env[CheckSpecEnv]
	if !ok {
		return nil
	}
	var cs CheckJobSpec
	if err := json.Unmarshal([]byte(raw), &cs); err != nil {
		return nil
	}
	cs.GitSecretID = id
	b, err := json.Marshal(cs)
	if err != nil {
		return err
	}
	check.Env[CheckSpecEnv] = string(b)
	return nil
}

// cloneRepoSpec is a deep copy of spec's maps, slices and check, so
// Readiness's output shares nothing a caller could edit with its input.
func cloneRepoSpec(spec RepoSpec) RepoSpec {
	out := spec
	out.Secrets = maps.Clone(spec.Secrets)
	out.BuildSecrets = slices.Clone(spec.BuildSecrets)
	out.Installation.Launchers = slices.Clone(spec.Installation.Launchers)
	out.Installation.Operators = slices.Clone(spec.Installation.Operators)
	out.Workflows = make(map[string]WorkflowSpec, len(spec.Workflows))
	for name, ws := range spec.Workflows {
		ws.Env = maps.Clone(ws.Env)
		ws.SecretEnv = maps.Clone(ws.SecretEnv)
		ws.Labels = maps.Clone(ws.Labels)
		ws.SecretIDs = maps.Clone(ws.SecretIDs)
		ws.BuildSecrets = slices.Clone(ws.BuildSecrets)
		out.Workflows[name] = ws
	}
	if spec.Check != nil {
		check := *spec.Check
		check.Env = maps.Clone(check.Env)
		check.SecretEnv = maps.Clone(check.SecretEnv)
		check.Workflows = slices.Clone(check.Workflows)
		out.Check = &check
	}
	return out
}

// hasEnabledVersion reports whether secret id has an enabled version; a
// missing secret has none.
func hasEnabledVersion(ctx context.Context, sm *secretmanager.Service, tg target, id string) (bool, error) {
	err := sm.Projects.Secrets.Versions.List("projects/"+tg.project+"/secrets/"+id).Pages(ctx, func(p *secretmanager.ListSecretVersionsResponse) error {
		for _, v := range p.Versions {
			if v.State == "ENABLED" {
				return errFound
			}
		}
		return nil
	})
	switch {
	case errors.Is(err, errFound):
		return true, nil
	case absent(err, serviceSecretManager, tg):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("listing the versions of secret %s: %w", id, err)
	}
	return false, nil
}

// hasLatest reports whether image (<registry path>/<package>:latest, in
// the repository's registry) has its latest tag. gcp.ImageName's package
// is one path component, so it needs no escaping in the tag's name.
func hasLatest(ctx context.Context, c *Clients, tg target, spec RepoSpec, image string) (bool, error) {
	rest, ok := strings.CutPrefix(image, spec.RegistryPath+"/")
	pkg, tagged := strings.CutSuffix(rest, ":latest")
	m := registryHostRE.FindStringSubmatch(spec.Installation.RegistryHost)
	if !ok || !tagged || pkg == "" || strings.ContainsAny(pkg, "/:@") || m == nil {
		return false, fmt.Errorf("image %s is not <registry>/<package>:latest in the repository's registry %s", image, spec.RegistryPath)
	}
	name := "projects/" + spec.GCPProject + "/locations/" + m[1] + "/repositories/" + spec.Registry.RepositoryID +
		"/packages/" + pkg + "/tags/latest"
	_, err := c.AR.Projects.Locations.Repositories.Packages.Tags.Get(name).Context(ctx).Do()
	switch {
	case absent(err, serviceArtifactRegistry, tg):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("reading tag %s: %w", name, err)
	}
	return true, nil
}

// hasRecord reports whether the runs bucket holds object key.
func hasRecord(ctx context.Context, c *Clients, tg target, bucket, key string) (bool, error) {
	_, err := c.Storage.Objects.Get(bucket, key).Context(ctx).Do()
	switch {
	// A disabled Storage API can't get here in practice (the state bucket
	// check and Terraform's backend need Storage first); if it did, the
	// record would read as missing and the schedule would stay paused.
	case absent(err, serviceStorage, tg):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("reading gs://%s/%s: %w", bucket, key, err)
	}
	return true, nil
}
