package infra

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/infra/tf"
)

// slugRE is the shape task.Slug gives: the readable part, then 16 hex
// digits. It is a path component of the workdir and the state prefix.
var slugRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?-[0-9a-f]{16}$`)

// RepoWorkdir is where fugaro update plans the repository slug of
// project: $XDG_STATE_HOME/fugaro/terraform/<project>/repos/<slug>, by
// default under ~/.local/state.
func RepoWorkdir(getenv func(string) string, project, slug string) (string, error) {
	if !slugRE.MatchString(slug) {
		return "", fmt.Errorf("%q is not a repository slug", slug)
	}
	inst, err := InstallationWorkdir(getenv, project)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(inst), "repos", slug), nil
}

// RepoStatePrefix is where the repository's state lives in the state
// bucket.
func RepoStatePrefix(slug string) string { return StatePrefixRepos + slug }

// RepoRootOptions are the repository root's inputs that come from flags.
type RepoRootOptions struct {
	// AllowJobDelete lowers the jobs' deletion protection, for offboarding.
	AllowJobDelete bool
}

// RepoRootVars is the repository root's terraform.tfvars.json: RepoVars,
// plus allow_job_delete, which is always written so that a run without
// --allow-job-delete puts the protection back.
func RepoRootVars(spec RepoSpec, o RepoRootOptions) ([]byte, error) {
	data, err := RepoVars(spec)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	doc["allow_job_delete"] = o.AllowJobDelete
	return sortedJSON(doc)
}

// InstallationStateExists reports whether the state bucket holds the
// installation's state: fugaro update has applied it there.
func InstallationStateExists(ctx context.Context, c *Clients, stateBucket string) (bool, error) {
	return StateExists(ctx, c, stateBucket, StatePrefixInstallation)
}

// StateExists reports whether the state bucket holds any object under the
// root's state prefix (StatePrefixInstallation, StatePrefixFirebase).
// Read-only.
func StateExists(ctx context.Context, c *Clients, stateBucket, statePrefix string) (bool, error) {
	found := false
	prefix := statePrefix + "/"
	err := c.Storage.Objects.List(stateBucket).Prefix(prefix).Pages(ctx, func(o *storage.Objects) error {
		if len(o.Items) > 0 {
			found = true
			return errFound
		}
		return nil
	})
	if err != nil && !errors.Is(err, errFound) {
		return false, fmt.Errorf("listing gs://%s/%s: %w", stateBucket, prefix, err)
	}
	return found, nil
}

// RepoStateObjects are the repository's state objects in the state
// bucket (its *.tfstate objects under RepoStatePrefix; a lock is not
// state), sorted.
func RepoStateObjects(ctx context.Context, c *Clients, stateBucket, slug string) ([]string, error) {
	if !slugRE.MatchString(slug) {
		return nil, fmt.Errorf("%q is not a repository slug", slug)
	}
	prefix := RepoStatePrefix(slug) + "/"
	var names []string
	err := c.Storage.Objects.List(stateBucket).Prefix(prefix).Pages(ctx, func(o *storage.Objects) error {
		for _, it := range o.Items {
			if isState(it.Name) {
				names = append(names, it.Name)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("listing gs://%s/%s: %w", stateBucket, prefix, err)
	}
	slices.Sort(names)
	return names, nil
}

// credentialsMemberAddress is where a repository root's state keeps the
// workflow module's invoker grant, one instance per workflow:
// modules/workflow/sa.tf's
// google_cloud_run_v2_service_iam_member.credentials, planned with count 0/1
// on that root's installation.credentials_service, so an address with this
// suffix is in the state exactly when that root was applied with the service
// on. The suffix is the whole address's tail — module.repo.module.workflow
// ["<name>"].google_cloud_run_v2_service_iam_member.credentials[0] — and it is
// the same spelling the spec's own address list carries (spec.go's
// repoAddresses), so a state and a spec name one resource the same way.
const credentialsMemberAddress = "google_cloud_run_v2_service_iam_member.credentials[0]"

// RepoCredentialsServiceApplied reports whether the repository root's STATE —
// the objects under RepoStatePrefix in the installation's state bucket, which
// belongs to the installation and not to one machine — holds that invoker
// grant.
//
// It is the shared evidence for "this repository's root has been applied with
// the credentials service on", where the tfvars a workdir holds
// (RepoCredentialsServiceDeployed) are only this machine's: the workdir is
// per-machine state (<state>/fugaro/terraform/<project>/repos/<slug>), so a
// repository whose root was applied from another machine, or from another
// checkout of this one, has no document here at all — which is exactly the
// installation an upgrade has to converge (internal/cli's
// backfillCredentialsServiceReady). The state bucket is the installation's
// one copy, written by whichever machine ran the apply, and its addresses are
// what that apply actually made rather than what a run handed Terraform: a
// root whose apply failed or was refused leaves a document ahead of its
// state, and the state is the one that counts.
//
// False is "this repository's state holds no such grant", and it is one
// answer for three states that are all "not evidence a job may invoke the
// service": a root never applied anywhere (no state object), one applied with
// the service off (the resource is not planned), and one whose state
// --forget removed. A state object that cannot be read, or that is not JSON,
// is the error, and no caller may read it as a no (Global Constraint 3).
func RepoCredentialsServiceApplied(ctx context.Context, c *Clients, stateBucket, slug string) (bool, error) {
	names, err := RepoStateObjects(ctx, c, stateBucket, slug)
	if err != nil {
		return false, err
	}
	for _, name := range names {
		data, err := ReadStateObject(ctx, c, stateBucket, name)
		if err != nil {
			return false, err
		}
		var st tf.State
		if err := json.Unmarshal(data, &st); err != nil {
			return false, fmt.Errorf("gs://%s/%s is not a state this program can read: %w", stateBucket, name, err)
		}
		for _, a := range st.Addresses() {
			if strings.HasSuffix(a, credentialsMemberAddress) {
				return true, nil
			}
		}
	}
	return false, nil
}

// StateBucketVersioned reports whether the state bucket keeps noncurrent
// versions, so a deleted state object can be restored.
func StateBucketVersioned(ctx context.Context, c *Clients, stateBucket string) (bool, error) {
	b, err := c.Storage.Buckets.Get(stateBucket).Context(ctx).Do()
	if err != nil {
		return false, fmt.Errorf("reading the state bucket gs://%s: %w", stateBucket, err)
	}
	return b.Versioning != nil && b.Versioning.Enabled, nil
}

// ForgetRepoState deletes the repository's state objects (by then empty:
// every address was removed from them), so the installation's rollback no
// longer finds the repository. The caller checks that the state bucket is
// versioned, so each stays recoverable as a noncurrent version. It returns
// what it deleted.
func ForgetRepoState(ctx context.Context, c *Clients, stateBucket, slug string) ([]string, error) {
	names, err := RepoStateObjects(ctx, c, stateBucket, slug)
	if err != nil {
		return nil, err
	}
	var deleted []string
	for _, n := range names {
		if err := c.Storage.Objects.Delete(stateBucket, n).Context(ctx).Do(); err != nil && !notFound(err) {
			return deleted, fmt.Errorf("deleting gs://%s/%s: %w", stateBucket, n, err)
		}
		deleted = append(deleted, n)
	}
	return deleted, nil
}

// SecretVersions reports, for each of the repository's secrets by logical
// name, whether the repository's job is covered: the repository's own copy
// has an enabled version, or -- when that copy has none -- a project-wide
// copy this repository may use does (spec.ProjectSecretCandidates). A secret
// that doesn't exist anywhere is covered by neither.
//
// The project-wide half is D2's (plan Task 2.1): storing at project scope is
// the default, so a name whose only copy is the project's is stored, not
// missing -- NeedsBuild would otherwise leave every workflow of a repository
// whose git credential was stored with `fugaro secrets set github-app-key`
// unbuildable, and its job would never be deployed. (bitbucket-token is never
// stored that way: it has no project-wide default, so its project copy is a
// repository's only through an explicit project_secrets entry, which is
// exactly when spec.ProjectSecretCandidates holds one.)
//
// It is the same question Readiness answers, through the same one helper
// (resolveSecretID), because both feed the same run: NeedsBuild's gate on the
// first image build and printMissing's "Still missing" steps read this map,
// while Readiness's own missing list reads the helper directly. Answering it
// repository-only here was Check 40 step 40i's bug: a project-covered name
// was listed as missing (with a fugaro secrets set --repo command whose copy
// would then win forever over the project's, the silent precedence flip
// project-wide secrets exist to avoid) and left the repository's first build
// unoffered, so a repository that was in fact ready ended as "left for you"
// and exited non-zero.
//
// A project-wide copy Readiness has already resolved for this repository
// (spec is the spec Readiness returned, ProjectSecrets its decision) counts
// too, and it is read back rather than resolved live again, for the reason
// appliedGitSecretID gives: the grants the apply just made were fixed for
// exactly that choice, so a second resolution could only disagree with them
// (and it would re-read a container Readiness already read).
func SecretVersions(ctx context.Context, c *Clients, spec RepoSpec) (map[string]bool, error) {
	tg := target{project: spec.GCPProject}
	has := func(id string) (bool, error) { return hasEnabledVersion(ctx, c.Secrets, tg, id) }
	out := make(map[string]bool, len(spec.Secrets))
	for _, logical := range slices.Sorted(maps.Keys(spec.Secrets)) {
		if spec.ProjectSecrets[logical] != "" {
			out[logical] = true
			continue
		}
		_, ok, err := resolveSecretID(has, spec.Secrets[logical], spec.ProjectSecretCandidates[logical])
		if err != nil {
			return nil, err
		}
		out[logical] = ok
	}
	return out, nil
}

// NeedsBuild are the workflows, sorted, whose first image build can go
// ahead: there is no build record yet (a new repository, or an adopted one
// whose image is still the bootstrap's), and every secret the workflow
// mounts has a version (versions, from SecretVersions). A checkout: clone
// workflow never needs one: it runs the installation's base image directly,
// and there is no per-repository image to build or push. Neither does a
// workflow a catalog entry gives its image to (CatalogEntry): a prebuilt
// entry's image is the operator's own reference, which no Fugaro build
// writes, and a built one belongs to the catalog's own build in the
// installation's shared registry — so a repository whose every workflow runs
// an entry has no per-repository image at all, and no first build to confirm
// (the same skip spec.go's daily check makes, on the same fact).
//
// An optional secret holds no build back (OptionalSecret): the spec in hand
// is the pre-readiness one, whose job still mounts the Anthropic key, but
// Readiness drops that mount when no version exists anywhere
// (internal/infra/readiness.go), so the job deploys without it and the image
// can be built. Gating on it here would strand every keyless provider-only
// repository: no build would ever be submitted, and the job it is meant to
// deploy could never come.
//
// spec.Workflows may hold several keys per fugaro.yaml workflow, one per
// Claude account the repository may choose (gcp.WorkflowVariant, design
// docs/design/named-oauth-tokens.md §4): they share one image, built once,
// not once per account, so only the default account's entry (the one whose
// key equals its own Name, the identity case of WorkflowVariant) is looked
// at here; the names this returns are always fugaro.yaml workflow names,
// never a variant key, which is what every caller (cfg.Workflows lookups,
// fugaro image build --workflow) needs.
func NeedsBuild(ctx context.Context, c *Clients, spec RepoSpec, versions map[string]bool) ([]string, error) {
	var out []string
	for _, key := range slices.Sorted(maps.Keys(spec.Workflows)) {
		ws := spec.Workflows[key]
		// The skips are clone's and the catalog's: clone runs the
		// installation's base image and builds nothing, and a catalog
		// entry's image is not this repository's own tag either (spec.go's
		// catalogJobImage routes such a job through the installation's
		// shared registry), so the record looked for below is one no build
		// of this repository would ever write. A checkout: shared workflow
		// does have an image to build — a dependency-layer one, shared with
		// every repository on the same key (design
		// forge-authorship-images/shared-images.md §6) — so it is reported
		// here like any other. Until C8b1's refresh builds that key, the
		// image this reports is the per-repository derived one the job still
		// carries (workflow() in spec.go).
		if key != ws.Name || ws.Checkout == config.CheckoutClone || ws.CatalogEntry != "" {
			continue
		}
		if slices.ContainsFunc(uniqueSecrets(ws), func(l string) bool { return !versions[l] && !OptionalSecret(l) }) {
			continue
		}
		ok, err := hasRecord(ctx, c, target{project: spec.GCPProject}, spec.Installation.RunsBucket, imagecheck.RecordKey(spec.Slug, ws.Name))
		if err != nil {
			return nil, err
		}
		if !ok {
			out = append(out, ws.Name)
		}
	}
	return out, nil
}

// SecretStep is one secret of a repository that has no version, and the
// command lines that store it.
type SecretStep struct {
	Name, What string
	// Optional marks a secret the job can go without (OptionalSecret): it is
	// not a gate, and the caller decides whether this run needs it at all
	// (config.AnthropicKeyNeeded) before printing the command or listing it
	// as missing.
	Optional bool
	// Commands are run in order, each one complete line: no chaining, no
	// comment, no value. The Claude token is made by the user first.
	Commands []string
}

// SecretFix is the one place that says where a missing logical secret's copy
// should be stored: project-wide when the installation's local config lets
// this repository use one (spec.ProjectSecretCandidates, ProjectSecretCandidate's
// own answer), because a repository copy would then win over it forever --
// the silent precedence flip project-wide secrets exist to avoid (design
// docs/design/project-secrets.md §5) -- and the repository's own copy
// otherwise. Readiness prints it as a Missing's fix; SecretCommands prints it
// as the command to type, and the two must not be able to disagree about the
// same name. bin names the binary and quote makes one shell word of a name,
// both as the caller prints commands.
//
// The project-wide answer is spelled as the bare command (D2, plan Task 2.1
// step 3b, the message half of finding 13): storing at project scope is what
// `secrets set <name>` does by default, so the line an operator is asked to
// type is the one that has to work -- `--project-wide` is still accepted as a
// synonym, and is what this function used to print, but it is no longer the
// spelling any message suggests. The eligibility answer stays spec's own
// (ProjectSecretCandidates), never a second one here.
func SecretFix(spec RepoSpec, logical, bin string, quote func(string) string) string {
	if _, ok := spec.ProjectSecretCandidates[logical]; ok {
		return bin + " secrets set " + quote(logical)
	}
	return bin + " secrets set " + quote(logical) + " --repo " + quote(spec.Name)
}

// SecretCommands are the fugaro secrets set steps of the repository's
// secrets that have no version anywhere, sorted by name: the value comes
// from stdin or a hidden prompt, never argv, and the Claude token is made
// by the user, in their own terminal. bin names the binary and quote makes
// one shell word of a name, both as the caller prints commands.
//
// Each step's command names the scope that name is stored at by default
// (D2): the project's for a name spec's own eligibility accepts
// project-wide, the repository's for the rest. A named Claude account's
// step is neither of those commands: `fugaro accounts add <name>` declares
// the account, stores its token where the shared job mounts it and
// converges the installation, so the scope is the command's own. It carries
// --repos with this repository (spec.Name), because the command would
// otherwise take the origin of whatever checkout it is pasted into, and
// --profile when the project has a profile that already serves the account
// (spec.AccountProfiles), since the command must name the profile whose
// claude_accounts gains the name, or refuse. When no profile serves the
// account the pair that mints the token and stores it stands instead: the
// account is one the config already declares, so `accounts add` would have
// nothing to declare and only a profile to choose for itself.
func SecretCommands(spec RepoSpec, versions map[string]bool, bin string, quote func(string) string) []SecretStep {
	var out []SecretStep
	for _, logical := range slices.Sorted(maps.Keys(spec.Secrets)) {
		if versions[logical] {
			continue
		}
		// The command is the one place that decides the scope (SecretFix),
		// read here as the same line Readiness prints as a Missing's fix: the
		// bare command for a name this repository may use project-wide (D2),
		// the repository-copy command otherwise.
		set := SecretFix(spec, logical, bin, quote)
		st := SecretStep{Name: logical, What: spec.Secrets[logical], Optional: OptionalSecret(logical)}
		switch {
		case logical == config.ClaudeOAuthToken:
			st.Commands = []string{"claude setup-token", set}
		case strings.HasPrefix(logical, config.ClaudeOAuthTokenPrefix):
			// A named account's token is the one command that declares the
			// account, stores the token project-wide -- where the shared job
			// mounts it -- and converges the installation (spec accounts-add
			// §8). "default" is reserved (config.ValidateClaudeAccounts), so
			// `accounts add` has no form for the default account above, and
			// every other name in this namespace is one it owns end to end.
			account := strings.TrimPrefix(logical, config.ClaudeOAuthTokenPrefix)
			// The profile whose claude_accounts already lists the account
			// (spec.AccountProfiles): it leaves that half of the command a
			// no-op. The command refuses to pick one in a project with several
			// (accountsAddProfile), and with exactly one it would add the
			// account to that profile and deploy a job, where storing the token
			// was the whole step.
			if profile := spec.AccountProfiles[account]; profile != "" {
				// --repos is the repository this root is for (spec.Name), and
				// not the origin of whatever checkout the line is pasted into:
				// accountsAddRepos defaults it to that origin, so the same line
				// run elsewhere would bind the account to the wrong repository —
				// and accountsAddBeyond measures the widening against that
				// checkout as well, so the confirmation would say nothing about
				// it. Naming this repository is a union with the list the
				// account already has, so it cannot narrow it.
				st.Commands = []string{bin + " accounts add " + quote(account) + " --repos " + quote(spec.Name) + " --profile " + quote(profile)}
				break
			}
			// No profile serves the account, and it is one this config already
			// declares (config.AllowedClaudeAccounts is where the name in
			// spec.Secrets came from), so `accounts add` would have nothing to
			// declare and only a profile to choose for itself. The pair that
			// mints the token and stores it in the container such a job would
			// mount is the whole step, as it was before `accounts add` took it
			// over.
			st.Commands = []string{"claude setup-token", set}
		default:
			st.Commands = []string{set + " < PATH-TO-THE-VALUE-FILE"}
		}
		out = append(out, st)
	}
	return out
}
