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
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
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
	// MissingNote is not a gate at all: something the run will need in one
	// case, reported so it is not a surprise later (the optional Anthropic
	// key: the job deploys without it, and a call that needs Anthropic is
	// refused until a key is stored).
	MissingNote MissingKind = "note"
	// MissingFirebase is a job that does not carry both the RTDB URL and the
	// Firebase web key — carries neither, or only one of the two — so a run
	// of it cannot read the project's policy
	// (D11, plan task 1.4 step 2). It gates the job only once a policy
	// exists: an installation that has not adopted the model is not
	// blocked, it is warned. The gate has two shapes, because a job's
	// deployment does: a job that does not exist yet is left undeployed
	// (this Missing, Deployed false), and one that already exists — which
	// nothing ever un-deploys (P7a) — is refused outright, in Readiness's
	// loop rather than here.
	MissingFirebase MissingKind = "firebase"
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
	// A note gates nothing, and says only itself: no job is mentioned, so
	// nobody reads it as one that didn't deploy.
	if m.Kind == MissingNote {
		return m.Reason
	}
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

// carriesFirebase reports whether a workflow's job carries what a run needs
// to read the project's policy: the database's address and the Firebase web
// key, both of which budgetEnv writes whenever the installation has them,
// whatever the budget mode (plan task 1.4 step 1 — the key is not optional:
// the token exchange needs it). A job that carries them is not promised a
// policy, only able to read one; whether one exists is Readiness's
// PolicyExists.
func carriesFirebase(ws WorkflowSpec) bool {
	return ws.Env[RTDBURLEnv] != "" && ws.Env[FirebaseAPIKeyEnv] != ""
}

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
		ws.Env[runner.SecretEnvsVar] = redactionEnvs(ws.SecretEnv)
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
//
// planOnly is fugaro init --repo's --plan-only, and it exempts the refusals
// that exist to stop a *change* rather than to describe a state: this function
// returns one, for an existing job this apply would deploy without the backend
// a published policy is read through (D11). A plan is read-only, so it may be
// shown whatever the apply would refuse — the same exemption init.go's
// planRepo applies to refuseClonesNotShipped and the --retire/--plan-only
// pair, and the reason is the same: an operator who cannot see the plan cannot
// fix what the refusal is about. What plan-only does not do is hide the
// problem: the gate above still reports MissingFirebase for that job, so the
// plan prints the job as one that cannot be deployed until the backend is
// configured.
func Readiness(ctx context.Context, c *Clients, spec RepoSpec, ex Existing, planOnly bool) (RepoSpec, []Missing, error) {
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
		// The command that would store a copy, from the one place that
		// decides where it belongs (SecretFix): the bare command when this
		// repository may use a project copy -- which D2 made the default
		// store, so it is what an operator is asked to type, and no longer
		// the --project-wide synonym (plan Task 2.1 step 3, the message
		// half of finding 13) -- and the repository-copy command otherwise.
		fix = SecretFix(spec, logical, "fugaro", func(s string) string { return s })
		cand, isCandidate := spec.ProjectSecretCandidates[logical]
		if !isCandidate {
			return false, fix, nil
		}
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
		// A credential the credentials service holds and the job has given up
		// is not mounted once the installation has opted in (D145, D146; C2.3,
		// review finding F1). The job's account keeps no accessor on it then
		// (modules/workflow/sa.tf), so a container that still declared the
		// mount would be created with a secret reference it cannot resolve —
		// the job is one Cloud Run refuses to create, and every run of that
		// installation fails. The grant and the mount go together, always.
		//
		// Which names those are is config.ServiceHeldSecretNames, and it is
		// empty on this branch: the GitHub App's private key joins it in the
		// release that also stops the job READING the key (C2.2, the runner's
		// fetch of the run's installation tokens from the service). Until then
		// the job keeps both its accessor and its mount, and a GitHub run of an
		// installation that opted in works exactly as it did before — which is
		// what the flip's absence has to mean, since C2.1's mint and C2.2's
		// fetch are not in this tree yet.
		//
		// It is dropped HERE rather than where the pre-readiness spec builds
		// its mounts, and that is the whole point of the placement: the App
		// key's CONTAINER, its operator grants and its project-wide resolution
		// are not the job's and must survive the drop — the build account reads
		// it to clone (`fugaro image build`, the daily check, D148/F3), the
		// service mints from it (C2.1), and an operator stores its versions.
		// Dropping the mount from the spec that feeds rs.Secrets,
		// ProjectSecretCandidates and NeedsBuild would take all four away with
		// it. Readiness is where the plan's mounts are decided (dropMount above
		// does the same for the optional Anthropic key), and the workflows'
		// jobs are the only containers it touches: the check job runs as the
		// BUILD account and keeps its mount (CheckSpec.SecretEnv, which the
		// build account's own accessor matches).
		//
		// With the opt-in off nothing is dropped, list or no list: the job
		// mounts exactly what it mounted before this package knew about the
		// service.
		//
		// The MODEL credentials' mounts are deliberately not dropped here yet:
		// C1.20 is the task that drops them ("no model secret mounted when the
		// service is on"), and it needs the runner to read each from the
		// service first (C1.15).
		if spec.Installation.CredentialsService != nil {
			for _, logical := range config.ServiceHeldSecretNames {
				dropMount(ws, logical)
			}
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
			// The repository copy has no version: the project-wide one may
			// still resolve it, optional secret or not, so this comes first
			// (design docs/design/project-secrets.md §5).
			resolved, fix, err := resolveProject(logical)
			if err != nil {
				return RepoSpec{}, nil, err
			}
			if resolved {
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
				//
				// Not blocked here is the point, but silently was not: with
				// the budget not enforcing nothing routes a call to a
				// provider (config.CheckProviderGateway refuses a provider
				// model in any other mode), so every call goes to Anthropic,
				// and a keyless run fails at agent start. Say so up front,
				// without gating the job (MissingNote): the deploy is right,
				// and the owner hears about the key here rather than from a
				// failed run. The mode is the job's own, the local config's
				// ceiling: a file that enforces under it only makes the note
				// unnecessary, never wrong, since the note never blocks.
				dropMount(ws, logical)
				if ws.Env[BudgetModeEnv] != localcfg.BudgetEnforce {
					missing = append(missing, Missing{Workflow: name, Kind: MissingNote, Reason: fmt.Sprintf(
						"%s has no version: the job deploys without it, and a run that calls Anthropic is refused until a key is stored (%s, then rerun fugaro init --repo)", logical, fix)})
				}
				continue
			}
			unresolved = append(unresolved, logical)
			reasons = append(reasons, gate{MissingSecret, fmt.Sprintf("secret %s has no version: %s", logical, fix)})
		}
		// D11: project-centric mode requires the Firebase backend, and there
		// is no other way to detect the mode (plan task 1.4 step 2). The
		// runner cannot see a policy without the run's own Firebase identity,
		// and a job deployed without the two variables below would run on the
		// environment ceiling with the project's policy unread and unenforced
		// — silently, because it cannot tell "no policy" from "no way to read
		// one". So once a policy has been published this gates the job;
		// before that it is a note, because an installation that has not
		// adopted the model is not blocked — and the gate is exact about
		// which "once" it means: PolicyExists is read through this machine's
		// own config (internal/cli's policyReadOnce, over budget.rtdb_url), so
		// a config that names no database address cannot see a policy at all
		// and this gate never fires for it, whatever the project holds. That
		// is the gap the note below states in full; the owner accepted it, and
		// what the gate does refuse is the config that can see the policy and
		// would deploy a job unable to read it.
		//
		// The note is for the installation that wants the shared budget and
		// has no backend to hold it: one whose jobs carry a mode that is not
		// off. An installation that leaves the budget off has chosen the
		// per-repository path, permanently (design §5(1)), and its runs are
		// told so by `fugaro doctor` — warning it about a model it is not
		// adopting would be noise on every apply. The exception is the last
		// branch below, which fires only for a job that HAS the backend in
		// the cloud: there the local config is what changed, and the note is
		// about damage this apply would do rather than about a model the
		// owner has not adopted.
		if !carriesFirebase(ws) {
			if ex.PolicyExists {
				reasons = append(reasons, gate{MissingFirebase, fmt.Sprintf(
					"the project has published a policy (%s), which only a job carrying %s and %s can read: set the project's Firebase backend (fugaro init --firebase <firebase-project-id>), then run fugaro init --repo for %s",
					projectpolicy.PathShared(), RTDBURLEnv, FirebaseAPIKeyEnv, spec.Name)})
			} else if ex.JobsFirebase[name] {
				// The job has the backend today and this apply would take it
				// away: the local config names no database address (or no
				// web key), so budgetEnv writes a job without either. That is
				// the one case with no other signal at all — the policy
				// cannot be read from a config that has no URL, so
				// ex.PolicyExists is false whatever the project has
				// published, and the jobs would silently stop reading a
				// policy that exists. Loud, but not a gate: the harm is
				// drift, and the fix is one command (L3 of the PR #379
				// review).
				//
				// This note is asked before the mode one below, because the
				// two are usually true together and this one is the one with
				// damage to name: losing budget.rtdb_url is not the same
				// decision as turning the budget off, so a config that has
				// lost the address still carries the mode its owner set
				// (budgetEnv writes it whenever it is not off), and the
				// generic note would otherwise answer with "nothing refuses
				// this deploy" for the very apply that is taking the backend
				// away from the jobs.
				missing = append(missing, Missing{Workflow: name, Kind: MissingNote, Reason: fmt.Sprintf(
					"workflow %s's job carries %s and %s today and this init --repo would remove them: the local config names no budget.rtdb_url (or no budget.firebase_api_key), so the job is deployed without the backend and every run of this repository stops reading the project's policy. Restore it with 'fugaro init --firebase <firebase-project-id>' (which writes budget.rtdb_url and budget.firebase_api_key), then run fugaro init --repo again",
					name, RTDBURLEnv, FirebaseAPIKeyEnv)})
			} else if ws.Env[BudgetModeEnv] != "" {
				missing = append(missing, Missing{Workflow: name, Kind: MissingNote, Reason: fmt.Sprintf(
					"this job's budget is on (%s=%s) but it does not carry both %s and %s, so a run of it could not read a project policy: configure the project's Firebase backend (fugaro init --firebase <firebase-project-id>, then fugaro init --repo). Which of the two the local config is missing decides what happens next, and the gate above is exact about it: a config that names budget.rtdb_url sees a published policy, so the day the project publishes one this job stops being deployable until it can read it; a config that names no database address sees none, whatever the project has published — that same config is what the gate reads the node through — so nothing refuses this deploy and the job goes out unable to read a policy that may well exist. That gap is the owner's, accepted on purpose. A launch is held to what the launcher's own config can see and no stricter rule: it refuses a policy it can see and cannot honour, never a budget mode on its own",
					BudgetModeEnv, ws.Env[BudgetModeEnv], RTDBURLEnv, FirebaseAPIKeyEnv)})
			}
		}
		current, exists := ex.Jobs[name]
		// D11's gate needs a second shape for a job that already exists, and
		// the review of PR #379 is why: DeployJob is never false for an
		// existing job (P7a, below), so the Missing above would be reported
		// while this apply deployed the very job it is about. A job that
		// exists is deployed by this apply whatever the gate says, so here
		// the refusal is the apply's: deploying it without the backend takes
		// the published policy away from every run of the repository, and
		// the operator learns at the next launch instead of here.
		if !planOnly && exists && ex.PolicyExists && !carriesFirebase(ws) {
			return RepoSpec{}, nil, userErr("the project has published a policy (%s), and this init --repo would deploy workflow %s's job without %s or %s in its environment: the job exists already, so it stays deployed and the policy it is bound by would stop being readable by every run of %s. Set the project's Firebase backend (fugaro init --firebase <firebase-project-id>) and run fugaro init --repo again",
				projectpolicy.PathShared(), name, RTDBURLEnv, FirebaseAPIKeyEnv, spec.Name)
		}
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
					gitSecretID = out.MountSecretID(logical)
				}
				continue
			}
			// B4: the check job mounts a subset of the same logical names,
			// and reads the same repo-wide local.secrets Terraform local, so
			// it resolves precedence identically -- which is why the ID it
			// names comes from MountSecretID, the one spelling of that
			// precedence, rather than from the maps here.
			resolved, fix, err := resolveProject(logical)
			if err != nil {
				return RepoSpec{}, nil, err
			}
			if resolved {
				if logical == gitLogical {
					gitSecretID = out.MountSecretID(logical)
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
				out.Check.Env[runner.SecretEnvsVar] = redactionEnvs(out.Check.SecretEnv)
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
