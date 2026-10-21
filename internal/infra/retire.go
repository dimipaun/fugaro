package infra

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	run "google.golang.org/api/run/v2"
	secretmanager "google.golang.org/api/secretmanager/v1"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra/tf"
)

// Retirement is one resource a repository's new configuration no longer
// produces: an address the state still manages that RepoSpec.Addresses does
// not name. It is a prediction for consent, computed in Go before Terraform
// runs -- never a substitute for the plan's own guard, which is what
// actually decides what an apply may delete.
type Retirement struct {
	Address string `json:"address"`
	Kind    string `json:"kind"`           // "secret container", "workflow job", "check job", "IAM member", …
	Cause   string `json:"cause"`          // the config change, in the user's words
	Kept    string `json:"kept,omitempty"` // what survives, or "" when nothing does

	// The fields the later steps need, which an address alone cannot give
	// (design §3.1). Retirements leaves them at their zero value; the
	// state's own attribute values fill them, through FillFromState.
	Logical   string `json:"logical,omitempty"`   // a secret container's logical name, "" otherwise
	SecretID  string `json:"secret_id,omitempty"` // its Secret Manager ID, "" otherwise
	Job       string `json:"job,omitempty"`       // a job's Cloud Run job name, "" otherwise
	Region    string `json:"region,omitempty"`    // its region, "" otherwise
	Protected bool   `json:"protected"`           // whether an ordinary apply can delete this address at all

	// Protected is NOT implied by the address alone: it is true for a
	// secret container (and for the registry, which carries prevent_destroy
	// too), and for a job whose state says deletion_protection = true. A job
	// whose protection is already false is one the apply deletes, and
	// state-removing it would leave it alive in GCP with nothing managing
	// it.
	//
	// ProtectedRead is whether that value was actually read, and the two
	// differ for exactly one input: a job whose state carries no
	// deletion_protection at all (or no readable values, or no row) is
	// Protected by the conservative default above, and no reader can tell
	// that apart from a state that said true until it asks this. Design §3.1
	// needs both directions: the *removal* set treats an unread attribute as
	// protected (never leaving a job alive), and the refusal *gate* refuses
	// only on a value it read as true -- it may not tell the user "the state
	// says deletion_protection = true" about an attribute the state does not
	// carry. Not part of the run's JSON: it is an input to the gate, not
	// something the document reports.
	ProtectedRead bool `json:"-"`
}

// KeptSecret is what a retired secret container leaves behind. The container
// is never destroyed: its module declares prevent_destroy, and the value it
// holds is not Terraform's to delete (design §4.2a). What it may NOT say is
// that the container is still there. A retired container address is one no
// workflow and no check job of the configuration mounts any more -- nothing
// about that says whether the container itself still exists in Secret
// Manager, and this is the one promise that has to hold either way:
// Readiness unions a live, correctly-labelled one back into the plan
// (Existing.ExtraSecrets, N4), so a container that is there survives, and
// one that is already gone stays gone.
const KeptSecret = "fugaro never deletes a secret or a secret version: this address leaves Terraform's state, and nothing in the plan or the Cloud Run API deletes the container"

// KeptRegistry is the same promise for the repository's image registry: it
// is the module's second prevent_destroy resource (registry.tf), and nothing
// in a plan deletes an image.
//
// Reached only from a state that holds the registry under a root this
// repository's modules do not use -- Addresses always produces
// module.repo.google_artifact_registry_repository.images, so that address is
// never in the set difference (review round 4). The row stays because the
// alternative direction is the dangerous one: a registry address this
// package cannot classify would be a retirement with no promise attached,
// and "the images stay" is exactly what an operator has to be told before
// anything leaves the state. TestRetirementsKeepsARegistryAddressUnderAnotherRoot
// pins it.
const KeptRegistry = "the registry and every image in it stay; Artifact Registry's own cleanup policy is the only thing that deletes one"

// Retirements is the addresses the state manages that spec no longer
// produces: a set difference, with no Terraform run at all. Both sides are
// computed by fugaro; the user types neither.
//
// It takes the state's addresses rather than a *tf.State so that it stays a
// pure function, testable without a Terraform binary. Everything the
// addresses cannot say -- a container's Secret Manager ID, a job's name and
// region, whether a job's deletion protection is on -- comes from the state
// through FillFromState.
func Retirements(state []string, spec RepoSpec) []Retirement {
	produced := make(map[string]bool)
	for _, a := range spec.Addresses() {
		produced[a] = true
	}
	var out []Retirement
	for _, a := range state {
		if produced[a] {
			continue
		}
		out = append(out, retirement(a, spec))
	}
	slices.SortFunc(out, func(a, b Retirement) int { return strings.Compare(a.Address, b.Address) })
	return slices.CompactFunc(out, func(a, b Retirement) bool { return a.Address == b.Address })
}

// retirement classifies one address the spec does not produce: what kind of
// resource it is, and which config change left it behind.
func retirement(addr string, spec RepoSpec) Retirement {
	r := Retirement{Address: addr}
	segs := addressSegments(addr)
	if len(segs) < 2 {
		return genericRetirement(r)
	}
	typ, name, key := resourceOf(segs)
	if variant, ok := workflowVariantOf(segs); ok {
		if _, live := spec.Workflows[variant]; !live {
			// The variant itself is gone (design rows A and C), so this is
			// the variant-swap retirement the whole flow is for.
			r.Kind = workflowKind(typ)
			r.Cause = variantCause(variant, spec)
			return r
		}
		// The variant is still in the configuration and only this one
		// resource of it changed -- the module stopped declaring it. Nothing
		// here may be called "workflow job": that kind is what the Cloud Run
		// API delete keys on (JobDeleteLabels), and the job of a workflow
		// that is still deployed must never be a candidate for it (M3). It
		// is a plain job, and what finishes it is the ordinary path's: the
		// API delete refuses this kind by rule, so a protected one leaves the
		// state and nothing then deletes it -- Removals lists it, the plan
		// cannot delete what is no longer in the state, and the summary names
		// it under "Removed from Terraform's state, and never deleted" --
		// while an unprotected one the plan deletes, which RetireAllowDelete
		// allows because the address is in the set.
		if r.Kind = workflowKind(typ); r.Kind == "workflow job" {
			r.Kind = "job"
			r.Cause = "the configuration no longer produces this job, though the workflow variant " + variant + " is still deployed"
			return r
		}
		if r.Kind == "secret accessor" {
			r.Cause = secretCause(key)
			return r
		}
		return genericRetirement(r)
	}
	switch typ {
	case "google_secret_manager_secret":
		if name != "this" {
			return genericRetirement(r)
		}
		r.Kind = "secret container"
		r.Cause = secretCause(key)
		r.Kept = KeptSecret
	case "google_secret_manager_secret_iam_member":
		r.Kind = "IAM member"
		// The operator grant's for_each key is "<logical> <member> <role>";
		// a build grant's is the logical name alone.
		logical := key
		if name == "operator" {
			logical, _, _ = strings.Cut(key, " ")
		}
		r.Cause = secretCause(logical)
	case "google_artifact_registry_repository":
		if name != "images" {
			return genericRetirement(r)
		}
		// Unreachable from RepoSpec.Addresses, which always produces this
		// address under the repo root: see KeptRegistry.
		r.Kind = "image registry"
		r.Cause = "the repository's registry path changed"
		r.Kept = KeptRegistry
	case "google_artifact_registry_repository_iam_member":
		r.Kind = "IAM member"
		if name == "operator_images" {
			r.Cause = memberCause(key, spec)
		}
	case "google_service_account":
		r.Kind = "service account"
	case "google_service_account_iam_member":
		r.Kind = "IAM member"
		if name == "operator_build" {
			r.Cause = memberCause(key, spec)
		}
	case "google_project_iam_member":
		r.Kind = "IAM member"
	case "google_storage_bucket_iam_member":
		r.Kind = "bucket grant"
	case "google_cloud_run_v2_job":
		if name != "check" {
			return genericRetirement(r)
		}
		r.Kind = "check job"
		r.Cause = checkCause(spec)
	case "google_cloud_run_v2_job_iam_member":
		if name != "check_invoker" {
			return genericRetirement(r)
		}
		r.Kind = "IAM member"
		r.Cause = checkCause(spec)
	case "google_cloud_scheduler_job":
		if name != "check" {
			return genericRetirement(r)
		}
		r.Kind = "Scheduler job"
		r.Cause = checkCause(spec)
	}
	return genericRetirement(r)
}

// genericRetirement fills the words an address this package cannot classify
// still gets: it is a retirement (the state manages it and the spec does
// not), and it says so rather than being dropped.
func genericRetirement(r Retirement) Retirement {
	if r.Kind == "" {
		r.Kind = "resource"
	}
	if r.Cause == "" {
		r.Cause = "the configuration no longer produces it"
	}
	return r
}

// workflowKind is the kind of a resource inside modules/workflow.
func workflowKind(typ string) string {
	switch typ {
	case "google_cloud_run_v2_job":
		return "workflow job"
	case "google_service_account":
		return "service account"
	case "google_secret_manager_secret_iam_member":
		return "secret accessor"
	case "google_storage_bucket_iam_member":
		return "bucket grant"
	}
	return "IAM member"
}

// variantCause is why a workflow variant retired, in the user's words. A
// variant key is gcp.WorkflowVariant's "<workflow>" or
// "<workflow>_<account>" -- a workflow name never contains "_" (its charset
// is what makes the join unambiguous) -- so the key says which of the two
// config changes it was: a named account that is no longer a variant this
// repository deploys (design row A), or a workflow that left fugaro.yaml
// altogether (row C).
func variantCause(key string, spec RepoSpec) string {
	names := map[string]bool{}
	for _, ws := range spec.Workflows {
		names[ws.Name] = true
	}
	if wf, acct, ok := strings.Cut(key, "_"); ok && names[wf] {
		return fmt.Sprintf("the named Claude account %s is no longer a variant this repository deploys", acct)
	}
	wf := key
	if i := strings.IndexByte(key, '_'); i >= 0 {
		wf = key[:i]
	}
	return fmt.Sprintf("workflow %s was removed from fugaro.yaml", wf)
}

// secretCause is why a secret container (or a grant on one) retired. The
// claude-oauth-token namespace is reserved for named Claude accounts
// (config.ClaudeAccountSecret), so a logical name in it is an agent
// credential and the change was the agent block's; anything else is a
// workflow's own secret.
func secretCause(logical string) string {
	if logical == config.ClaudeAccountSecret(config.DefaultClaudeAccount) || strings.HasPrefix(logical, config.ClaudeOAuthTokenPrefix) {
		return "agent.auth no longer mounts " + logical
	}
	return "no workflow mounts " + logical + " any more"
}

// memberCause is why a grant keyed on a member retired: the member is no
// longer one this installation grants it to. Empty when the member is still
// there, which leaves the generic words in place rather than a wrong reason.
func memberCause(member string, spec RepoSpec) string {
	if slices.Contains(launchersWithOperators(spec.Installation.Launchers, spec.Installation.Operators), member) {
		return ""
	}
	return "the member " + member + " is no longer an operator or launcher of this installation"
}

// checkCause is why the daily check's job, invoker grant and Scheduler job
// retired. The rule is the spec's own (design §3.1a): the check job is
// produced only while some workflow is checked and the gate lets it deploy,
// so no check in the spec at all is the last checked workflow having left.
//
// The per-workflow case -- one workflow stops being checked while others
// still are -- never reaches here: the check job's address is unchanged, so
// it is an update to the same job, not a retirement. Which workflow left is
// in the state's own FUGARO_CHECK_SPEC, not in the spec, and nothing here
// guesses it.
func checkCause(spec RepoSpec) string {
	if spec.Check == nil {
		return "no workflow is checked any more (rebuild.check: off everywhere)"
	}
	return "the daily image check is not deployed any more"
}

// FillFromState fills Logical, SecretID, Job, Region and Protected for every
// Retirement from the state's own resource attribute values, walking the
// exported resources of st.Values.RootModule and its children (the same raw
// JSON tf.State keeps, decoded here so that State.Addresses's narrower
// decode stays as it is). A field that cannot be read is left empty, never
// guessed.
//
// Protected is the one field whose empty value is not "unknown but
// harmless": a job whose values cannot be read at all counts as protected,
// the conservative direction. Removals then takes it out of the state and
// the API delete finishes it, and that delete works whatever the job's
// deletion_protection says -- so the conservative reading can never leave a
// resource alive, while the optimistic one can. ProtectedRead is what keeps
// that default from reaching the refusal gate, which may refuse only on a
// value the state actually carries (design §3.1).
func FillFromState(st *tf.State, rets []Retirement) {
	// Every field is decided by the address, never by the Kind words: the
	// same walk serves a caller that built its Retirements by hand.
	kind := make(map[int]string, len(rets))
	for i := range rets {
		segs := addressSegments(rets[i].Address)
		if len(segs) < 2 {
			continue
		}
		typ, name, key := resourceOf(segs)
		switch {
		case typ == "google_secret_manager_secret" && name == "this":
			// The for_each key is the logical name, and prevent_destroy is
			// the module's, not an attribute: both are the address's own.
			kind[i] = "container"
			rets[i].Logical = key
			rets[i].Protected = true
		case typ == "google_artifact_registry_repository" && name == "images":
			// The module's second prevent_destroy resource.
			rets[i].Protected = true
		case typ == "google_cloud_run_v2_job":
			// A job counts as protected until the state's own
			// deletion_protection says otherwise, and that default is set
			// here, before the state is read: a state that is nil, has no
			// values, or does not hold the job at all leaves it protected
			// (see above).
			kind[i] = "job"
			rets[i].Protected = true
		}
	}
	if st == nil || st.Values == nil {
		return
	}
	at := make(map[string]int, len(rets))
	for i, r := range rets {
		at[r.Address] = i
	}
	var walk func(m tf.StateModule)
	walk = func(m tf.StateModule) {
		for _, raw := range m.Resources {
			var res struct {
				Address string          `json:"address"`
				Values  json.RawMessage `json:"values"`
			}
			if err := json.Unmarshal(raw, &res); err != nil {
				continue
			}
			i, ok := at[res.Address]
			if !ok {
				continue
			}
			fillFromValues(&rets[i], kind[i], res.Values)
		}
		for _, c := range m.ChildModules {
			walk(c)
		}
	}
	walk(st.Values.RootModule)
}

// fillFromValues reads one resource's own attributes: a job's name, location
// and deletion_protection, and a container's secret_id. The names are the
// provider schema's, and a value that is not there leaves its field empty --
// a job's protection is left as the protective default it was given.
func fillFromValues(r *Retirement, kind string, values json.RawMessage) {
	if len(values) == 0 {
		return
	}
	var v struct {
		Name               *string `json:"name"`
		Location           *string `json:"location"`
		SecretID           *string `json:"secret_id"`
		DeletionProtection *bool   `json:"deletion_protection"`
	}
	if err := json.Unmarshal(values, &v); err != nil {
		return
	}
	switch kind {
	case "job":
		if v.Name != nil {
			r.Job = *v.Name
		}
		if v.Location != nil {
			r.Region = *v.Location
		}
		if v.DeletionProtection != nil {
			r.Protected = *v.DeletionProtection
			// The value was read: only now can the gate say the state
			// carries it (design §3.1's refusal rule).
			r.ProtectedRead = true
		}
	case "container":
		if v.SecretID != nil {
			r.SecretID = *v.SecretID
		}
	}
}

// addressSegments splits a resource address at its top-level dots, so a
// bracketed for_each key or count index never splits a segment.
func addressSegments(addr string) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(addr); i++ {
		switch addr[i] {
		case '[':
			depth++
		case ']':
			depth--
		case '.':
			if depth == 0 {
				out = append(out, addr[start:i])
				start = i + 1
			}
		}
	}
	return append(out, addr[start:])
}

// resourceOf splits the last two segments of an address into the resource
// type, its name and its instance key ("" for a resource with neither
// for_each nor count).
func resourceOf(segs []string) (typ, name, key string) {
	name, key = splitIndex(segs[len(segs)-1])
	return segs[len(segs)-2], name, key
}

// splitIndex splits "name[key]" into "name" and the key, unquoted for a
// for_each key and as written for a count index, and "name" into "name" and
// "".
func splitIndex(s string) (string, string) {
	i := strings.IndexByte(s, '[')
	if i < 0 || !strings.HasSuffix(s, "]") {
		return s, ""
	}
	name, raw := s[:i], s[i+1:len(s)-1]
	if key, err := strconv.Unquote(raw); err == nil {
		return name, key
	}
	return name, raw
}

// workflowVariantOf is the workflow variant key of an address inside
// modules/workflow, and false for an address that is not in one.
func workflowVariantOf(segs []string) (string, bool) {
	for i := 1; i+2 < len(segs); i++ {
		if segs[i-1] != "module" {
			continue
		}
		if name, key := splitIndex(segs[i]); name == "workflow" && key != "" {
			return key, true
		}
	}
	return "", false
}

// Removals is the addresses in rets that must leave Terraform's state BEFORE
// the plan, sorted -- what a retirement passes to terraform state rm. It is
// NOT the whole retirement set, and that is the point: only the addresses an
// ordinary apply CANNOT delete qualify, and both halves of that test are
// per-address facts (design §4.2a):
//
//   - still in stateAddrs, so a rerun never passes an address that is gone
//     (terraform state rm of one fails with "No matching objects found");
//   - Protected: a secret container carries prevent_destroy, which aborts
//     the plan before it lists anything, and a Cloud Run job whose state says
//     deletion_protection = true cannot be deleted by an apply once its
//     resource has left the configuration.
//
// A retired job whose state says deletion_protection = false is NOT
// returned: it is exactly the job an apply must delete (the user who
// followed gcp-setup.md's order), and taking its address out here would
// leave it alive in GCP with nothing managing it. The same goes for
// everything else a retired variant owned -- its service account, its IAM
// members, its bucket grant -- which stays in state so the plan deletes it.
func Removals(stateAddrs []string, rets []Retirement) []string {
	inState := make(map[string]bool, len(stateAddrs))
	for _, a := range stateAddrs {
		inState[a] = true
	}
	var out []string
	for _, r := range rets {
		// An address that is empty or could be read as a flag is never
		// passed on, whatever produced it: terraform state rm would refuse
		// it, and fugaro must not be the thing that hands it over.
		if r.Address == "" || strings.HasPrefix(r.Address, "-") {
			continue
		}
		if r.Protected && inState[r.Address] {
			out = append(out, r.Address)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// RetireAllowDelete is the guard's allow-list for p: each delete or replace
// of an address in rets (the retirement set of design §3.1a), and nothing
// else. A replace is ["delete","create"], so it is a delete of an address in
// the set; anything else the plan does to a retired address is not a delete
// and needs no permission.
//
// An address the retirement set does not name is never allowed, however the
// plan came by it: the guard still refuses it (Review Focus 1).
//
// A kept row is never allowed either, and that is the third wall of
// "Fugaro never deletes a secret or a secret version" (design §6): a secret
// container and the registry are the two addresses whose module declaration
// carries lifecycle.prevent_destroy, so no plan may delete them and this
// allow-list may not be the thing that lets one try. Nothing reaches the
// guard with such a delete today -- Terraform aborts the plan on the block
// first -- which is exactly why the wall is one line here rather than a
// mechanism: a plan that somehow carried the delete is refused, not applied.
// The rows a retirement really deletes are the ones with no Kept text
// (grants, jobs, service accounts), and they are unaffected.
func RetireAllowDelete(p *tf.Plan, rets []Retirement) []string {
	if p == nil {
		return nil
	}
	inSet := make(map[string]bool, len(rets))
	for _, r := range rets {
		if r.Kept != "" {
			continue
		}
		inSet[r.Address] = true
	}
	var out []string
	for _, rc := range p.ResourceChanges {
		if inSet[rc.Address] && slices.Contains(rc.Change.Actions, "delete") {
			out = append(out, rc.Address)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// StateRmFunc is (*tf.TF).StateRm: the one terraform call a retirement's
// state removal makes. It is an interface here so that the removal's own
// behaviour -- one call per address, in order, and a vanished address
// tolerated -- is testable without a terraform binary.
type StateRmFunc func(ctx context.Context, addrs ...string) error

// noMatchingObjects is what terraform prints for `state rm` of an address
// the state no longer holds. The tolerance keys on the command as well as
// the text, so nothing else can be swallowed by it.
const noMatchingObjects = "No matching objects found"

// RemoveFromState removes addrs from Terraform's state, one call per
// address and in order, and reports which of them the state had already
// lost. Nothing is destroyed: state rm leaves the live resource exactly as
// it was, which is what makes a state-removed secret re-adoptable by label
// and a state-removed job deletable through the API.
//
// This is the second line of defence of the resume path, not the first:
// Removals returns only protected addresses still in the state, so an
// address an earlier run already removed is normally never passed here at
// all. But one can still vanish between the state read and the call -- a
// hand-run `terraform state rm` in another terminal, or a partial earlier
// failure -- and terraform then fails the whole call. So the call is made
// per address, and exactly terraform's own "No matching objects found"
// from `state rm` is reported as already gone instead of as a failure.
// Neither defence replaces the other.
//
// The two halves are returned as they are reached, so a real failure still
// tells the caller which addresses were removed before it.
func RemoveFromState(ctx context.Context, stateRm StateRmFunc, addrs []string) (removed, gone []string, err error) {
	for _, a := range addrs {
		if err := stateRm(ctx, a); err != nil {
			if alreadyGone(err) {
				gone = append(gone, a)
				continue
			}
			return removed, gone, fmt.Errorf("removing %s from the state: %w", a, err)
		}
		removed = append(removed, a)
	}
	return removed, gone, nil
}

// alreadyGone is whether err is terraform's own report that `state rm`
// found no object for an address. A message that merely contains the words,
// from another command or from somewhere else entirely, is not.
func alreadyGone(err error) bool {
	var xe *tf.ExitError
	if !errors.As(err, &xe) {
		return false
	}
	return xe.Command == "state rm" && strings.Contains(xe.Stderr, noMatchingObjects)
}

// --- Task 4: the orphan set, read from the labels -------------------------
//
// The state alone cannot answer the question a rerun asks. Retirements is
// state − spec, and the retirement's first step removes addresses from the
// state (design §4.2a), so after a failed run the orphaned job is in no
// state at all. The labels are the second source of truth (design §3.1a):
// discovery's own rule -- a resource under one of our names that carries our
// mark is ours -- applied to the resources that outlive the configuration.

// OrphanLabels are the labels every resource of this repository carries
// (v1.md §8.2). They are also the secrets.list filter, which takes the one
// shape both the real API and the fake do.
func OrphanLabels(spec RepoSpec) map[string]string {
	return map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: spec.Label}
}

// labelFilter is OrphanLabels as a list filter, in the shape
// discover.go's extraSecrets already sends.
func labelFilter(labels map[string]string) string {
	var out []string
	for _, k := range slices.Sorted(maps.Keys(labels)) {
		out = append(out, "labels."+k+"="+labels[k])
	}
	return strings.Join(out, " AND ")
}

// Orphans are the cloud resources the labels say are ours that spec no
// longer produces, in two groups:
//
//   - retirements: a workflow job whose fugaro_workflow is no longer a
//     variant key of spec.Workflows, the check job (fugaro_role=check) when
//     spec.Check is nil, and a labelled secret container whose logical name
//     spec no longer mounts while the state still holds its address.
//   - notes: a labelled secret container whose logical name is no longer
//     mounted and whose address the state no longer holds. Informational
//     only: RetireSet never counts one as a retirement, so a kept container
//     an earlier run released can never make the summary, the refusal gate
//     or --retire fire again. Without this split every later
//     `fugaro update` would re-report the two OAuth tokens forever,
//     because a kept container is forgotten by design (design §3.1a).
//
// stateAddrs are the addresses the state holds, which is the only thing that
// tells the two groups apart: a container the *current* retirement releases
// has its address in the state while the run starts, and one an earlier
// retirement already released is remembered by nothing but its labels.
//
// It is read-only -- one jobs.list, one secrets.list with a label filter --
// and runs under --plan-only too. A label call that is refused degrades
// loudly rather than silently (design §3.1a): the warning names the call,
// the permission and the gcloud command that answers the question by hand,
// the run continues with the state-derived set, and "nothing to retire" and
// "could not look" stay different sentences. A refused read narrows what the
// run can see; it never makes it see something that is not there. The error
// is for a context that was abandoned, which is the run giving up rather
// than a permission the operator lacks.
func Orphans(ctx context.Context, c *Clients, spec RepoSpec, stateAddrs []string) (rets, notes []Retirement, warnings []string, err error) {
	jobs, warn, err := orphanJobs(ctx, c, spec)
	if err != nil {
		return nil, nil, nil, err
	}
	if warn != "" {
		warnings = append(warnings, warn)
	}
	rets = append(rets, jobs...)

	containers, notes, warn, err := orphanContainers(ctx, c, spec, stateAddrs)
	if err != nil {
		return nil, nil, nil, err
	}
	if warn != "" {
		warnings = append(warnings, warn)
	}
	rets = append(rets, containers...)
	slices.SortFunc(rets, byAddress)
	slices.SortFunc(notes, byAddress)
	return rets, notes, warnings, nil
}

// byAddress orders retirements, so a set built from two sources reads the
// same however the cloud listed them.
func byAddress(a, b Retirement) int { return strings.Compare(a.Address, b.Address) }

// orphanJobs are the labelled Cloud Run jobs of this repository that spec no
// longer produces. The two kinds carry different labels and so get one rule
// each (design §3.1a, review B2): a workflow job is judged by
// fugaro_workflow -- the variant key -- and the check job by fugaro_role,
// which it carries in place of a workflow label. A rule that asked every job
// for fugaro_workflow would get the check job wrong in both directions: it
// would call a live check job an orphan on every run while the check is
// still wanted, and it would leave a genuinely retired one alive forever.
func orphanJobs(ctx context.Context, c *Clients, spec RepoSpec) ([]Retirement, string, error) {
	parent := "projects/" + spec.GCPProject + "/locations/" + spec.Region
	var out []Retirement
	err := c.Run.Projects.Locations.Jobs.List(parent).Pages(ctx, func(p *run.GoogleCloudRunV2ListJobsResponse) error {
		for _, j := range p.Jobs {
			if !hasMarks(j.Labels, OrphanLabels(spec)) {
				continue
			}
			if r, ok := orphanJob(j, spec); ok {
				out = append(out, r)
			}
		}
		return nil
	})
	if err != nil {
		warn, fatal := labelCallFailed(ctx, "the repository's Cloud Run jobs", "run.jobs.list",
			"gcloud run jobs list --project "+spec.GCPProject+" --region "+spec.Region, err)
		return nil, warn, fatal
	}
	return out, "", nil
}

// orphanJob is one labelled job of this repository as a retirement, and
// whether it is one at all: a job of a variant the spec still builds is not,
// whatever the state says, and neither is the check job while the check is
// still wanted.
func orphanJob(j *run.GoogleCloudRunV2Job, spec RepoSpec) (Retirement, bool) {
	name := lastSegment(j.Name)
	switch variant := j.Labels[gcp.LabelWorkflow]; {
	case variant != "":
		if _, live := spec.Workflows[variant]; live {
			return Retirement{}, false
		}
		return Retirement{
			Address: workflowJobAddress(variant), Kind: "workflow job", Cause: variantCause(variant, spec),
			Job: name, Region: spec.Region,
			// Out of the configuration, so no apply can lower its
			// deletion_protection any more: the API delete is the only
			// thing that finishes it (design §4.2c).
			Protected: true,
		}, true
	case j.Labels[gcp.LabelRole] == gcp.RoleCheck && spec.Check == nil:
		return Retirement{
			Address: checkJobAddress(), Kind: "check job", Cause: checkCause(spec),
			Job: name, Region: spec.Region, Protected: true,
		}, true
	}
	return Retirement{}, false
}

// mountedSecrets are the logical secrets the spec's own jobs mount: every
// workflow's mounts and the check job's, read off the spec's own SecretEnv
// maps. It is the pre-union answer to "the configuration still produces
// it" -- Readiness's union of the live containers goes into RepoSpec.Secrets
// and never into a mount (readiness.go's N4 rule) -- and it is the question
// §3.1a asks of a labelled container, never the map: the map holds every
// correctly-labelled live container whether anything mounts it or not, so
// asking it would make this call's answer a tautology.
func mountedSecrets(spec RepoSpec) map[string]bool {
	out := make(map[string]bool)
	for _, ws := range spec.Workflows {
		for _, logical := range ws.SecretEnv {
			out[logical] = true
		}
	}
	if spec.Check != nil {
		for _, logical := range spec.Check.SecretEnv {
			out[logical] = true
		}
	}
	return out
}

// orphanContainers are the labelled secret containers of this repository
// that no workflow and no check job of the configuration mounts any more
// (design §3.1a). The call is a new one: discovery Gets each logical name of
// the spec, so it can only ever see a container the new spec still produces,
// which is exactly what an orphan is not.
//
// The test is the mounts, not RepoSpec.Secrets, and the difference is the
// whole of this call: Readiness has already unioned every correctly-labelled
// live container back into spec.Secrets (Existing.ExtraSecrets, N4 -- the
// project-secrets rule that a config edit may never shrink the module's
// for_each), so a container the labels find is always a key of that map, and
// a test against the map would skip every one of them. A container the state
// no longer holds is a note: a secret an earlier retirement already released,
// which is what makes a rerun name it instead of forgetting it.
//
// The union has a second consequence, and it is what keeps the released group
// to what the design says it is. The same union that puts the logical name
// back into spec.Secrets puts the address back into spec.Addresses(), so
// DiscoverRepo writes an import for the container and the plan re-produces
// it: the run that would call it a retirement is the run that adopts it, and
// removing it from the state would leave it unmanaged if the run stopped
// before the apply. Only a container whose address the spec does not produce
// can be released -- design §3.2's KEPT case, "a container the label list did
// not find, or did not find correctly labelled" -- so this branch is the
// conservative floor for a caller whose spec has not been through Readiness
// rather than a path the CLI's own run takes. The note branch below is
// unaffected either way: a note is informational, and it names a container
// the same run does adopt.
func orphanContainers(ctx context.Context, c *Clients, spec RepoSpec, stateAddrs []string) (rets, notes []Retirement, warn string, err error) {
	inState := make(map[string]bool, len(stateAddrs))
	for _, a := range stateAddrs {
		inState[a] = true
	}
	produced := make(map[string]bool)
	for _, a := range spec.Addresses() {
		produced[a] = true
	}
	mounted := mountedSecrets(spec)
	err = c.Secrets.Projects.Secrets.List("projects/"+spec.GCPProject).
		Filter(labelFilter(OrphanLabels(spec))).
		Pages(ctx, func(p *secretmanager.ListSecretsResponse) error {
			for _, s := range p.Secrets {
				logical := s.Labels[gcp.LabelSecret]
				if logical == "" {
					continue
				}
				if mounted[logical] {
					continue
				}
				r := Retirement{
					Address: secretContainerAddress(logical), Kind: "secret container", Cause: secretCause(logical),
					Kept: KeptSecret, Logical: logical, SecretID: lastSegment(s.Name), Protected: true,
				}
				// Produced means adopted: the union put the logical name
				// back into spec.Secrets, so the plan re-produces this
				// address and the state row belongs to the resource the
				// plan keeps. Nothing is released and nothing leaves the
				// state, so it is a note, not a retirement.
				if inState[r.Address] && !produced[r.Address] {
					rets = append(rets, r)
					continue
				}
				notes = append(notes, r)
			}
			return nil
		})
	if err != nil {
		warn, fatal := labelCallFailed(ctx, "the repository's secrets", "secretmanager.secrets.list",
			"gcloud secrets list --project "+spec.GCPProject+" --filter='"+labelFilter(OrphanLabels(spec))+"'", err)
		return nil, nil, warn, fatal
	}
	return rets, notes, "", nil
}

// labelCallFailed is what a refused or failed label call is: a warning that
// names the call, the permission and the gcloud command that answers the
// question by hand, and nothing at all when the run itself was abandoned.
func labelCallFailed(ctx context.Context, what, permission, gcloud string, err error) (warning string, fatal error) {
	if ctx.Err() != nil {
		return "", fmt.Errorf("listing %s: %w", what, err)
	}
	return fmt.Sprintf("could not list %s (%s): %v; orphaned resources cannot be checked, so anything the state no longer holds will not be named. Check by hand with: %s",
		what, permission, err, gcloud), nil
}

// workflowJobAddress and checkJobAddress are the addresses the modules give
// the two kinds of job, so an orphan read from the labels deduplicates
// against the state's own row for the same resource instead of being
// reported twice.
func workflowJobAddress(variant string) string {
	return "module.repo.module.workflow[" + strconv.Quote(variant) + "].google_cloud_run_v2_job.this[0]"
}

func checkJobAddress() string { return "module.repo.google_cloud_run_v2_job.check[0]" }

// secretContainerAddress is the address modules/repo/secrets.tf gives the
// container of a logical name.
func secretContainerAddress(logical string) string {
	return "module.repo.google_secret_manager_secret.this[" + strconv.Quote(logical) + "]"
}

// lastSegment is the name a full resource name ends in.
func lastSegment(name string) string { return name[strings.LastIndex(name, "/")+1:] }

// RetireSet is the retirement set of design §3.1a: Orphans' retirements
// unioned with the state-derived rows, deduplicated by address. Orphans wins
// on the fields it read from the live resource -- Job, Region, SecretID,
// Logical -- and nothing else: Protected, Kept, Kind and Cause stay the
// state-derived row's, because the state is what says whether an ordinary
// apply can delete the address at all.
//
// Orphans' notes are never part of it, and that is the whole point of the
// split: a kept container an earlier retirement already released cannot make
// the summary print, cannot open the refusal gate and cannot make --retire
// the next command.
func RetireSet(orphans, retired []Retirement) []Retirement {
	out := make([]Retirement, 0, len(orphans)+len(retired))
	at := make(map[string]int, len(orphans)+len(retired))
	for _, r := range retired {
		if r.Address == "" {
			continue
		}
		if _, dup := at[r.Address]; dup {
			continue
		}
		at[r.Address] = len(out)
		out = append(out, r)
	}
	for _, o := range orphans {
		i, ok := at[o.Address]
		if !ok {
			at[o.Address] = len(out)
			out = append(out, o)
			continue
		}
		for _, f := range []struct{ live, into *string }{
			{&o.Job, &out[i].Job}, {&o.Region, &out[i].Region},
			{&o.SecretID, &out[i].SecretID}, {&o.Logical, &out[i].Logical},
		} {
			if *f.live != "" {
				*f.into = *f.live
			}
		}
	}
	slices.SortFunc(out, byAddress)
	return out
}

// RetireSplit sorts a retirement set by what actually happens to each
// address, which is what the summary's counts and the API delete's own loop
// both read (design §3.2). The three groups are exclusive and are decided by
// facts, never by the address's kind:
//
//   - api: the rows the Cloud Run API deletes after the apply. A job is one
//     when its own labels name it (JobDeleteLabels) AND the apply cannot
//     delete it: either it is state-removed and the delete is what finishes
//     it, or the state does not hold it at all (the rerun after a failed
//     run, where the labels are the only record left). A job the state still
//     holds with deletion_protection = false is NOT one: the plan deletes
//     it, and calling the API on it as well would be a second delete of a
//     resource the apply already took.
//   - kept: the rows nothing deletes (Kept is set: a secret container, the
//     registry).
//   - apply: everything else -- the addresses the plan's own delete list
//     covers.
func RetireSplit(stateAddrs []string, spec RepoSpec, rets []Retirement) (api, apply, kept []Retirement) {
	inState := make(map[string]bool, len(stateAddrs))
	for _, a := range stateAddrs {
		inState[a] = true
	}
	for _, r := range rets {
		switch {
		case r.Kept != "":
			kept = append(kept, r)
		case apiDeleted(r, spec, inState):
			api = append(api, r)
		default:
			apply = append(apply, r)
		}
	}
	return api, apply, kept
}

// apiDeleted is whether the Cloud Run API delete is what finishes r: a job
// whose labels name it and that no apply can delete -- out of the state
// already, or state-removed because its protection is still on.
func apiDeleted(r Retirement, spec RepoSpec, inState map[string]bool) bool {
	if _, ok := JobDeleteLabels(spec, r); !ok {
		return false
	}
	return !inState[r.Address] || r.Protected
}

// HasMarks reports whether have carries every pair in want: the label rule
// discovery adopts by and a delete checks before it runs. It is hasMarks
// (discover.go) exported for the retirement's API delete, which lives in
// internal/cli and must apply the same rule Orphans applies here -- the two
// kinds of job carry different labels, and a delete judged by the wrong one
// either refuses a legitimate delete or takes another variant's job.
func HasMarks(have, want map[string]string) bool { return hasMarks(have, want) }

// JobDeleteLabels is the label set the API delete must find on the live job
// of a retired address, and whether there is one: the fugaro mark and this
// repository's label, plus fugaro_workflow=<the retired variant> for a
// workflow job, or fugaro_role=check for the check job -- which carries no
// fugaro_workflow at all (modules/repo/check.tf:22-26). A check job judged
// by a workflow label would be refused forever; a workflow job judged by
// fugaro_role alone would take a surviving variant's job (design §4.2c,
// review B2).
func JobDeleteLabels(spec RepoSpec, r Retirement) (map[string]string, bool) {
	want := OrphanLabels(spec)
	switch r.Kind {
	case "workflow job":
		variant, ok := workflowVariantOf(addressSegments(r.Address))
		if !ok {
			return nil, false
		}
		want[gcp.LabelWorkflow] = variant
	case "check job":
		want[gcp.LabelRole] = gcp.RoleCheck
	default:
		return nil, false
	}
	return want, true
}
