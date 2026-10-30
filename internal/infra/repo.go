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

	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/imagecheck"
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
// and there is no per-repository image to build or push.
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
		// The skip is clone's alone: it runs the installation's base image
		// and builds nothing. A checkout: shared workflow does have an image
		// to build — a dependency-layer one, shared with every repository on
		// the same key (design forge-authorship-images/shared-images.md §6)
		// — so it is reported here like any other.
		if key != ws.Name || ws.Checkout == config.CheckoutClone {
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
// project-wide, the repository's for the rest.
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
		if logical == "claude-oauth-token" {
			st.Commands = []string{"claude setup-token", set}
		} else {
			st.Commands = []string{set + " < PATH-TO-THE-VALUE-FILE"}
		}
		out = append(out, st)
	}
	return out
}
