package infra

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra/tf"
)

// Retirement is one resource a repository's new configuration no longer
// produces: an address the state still manages that RepoSpec.Addresses does
// not name. It is a prediction for consent, computed in Go before Terraform
// runs -- never a substitute for the plan's own guard, which is what
// actually decides what an apply may delete.
type Retirement struct {
	Address string
	Kind    string // "secret container", "workflow job", "check job", "IAM member", …
	Cause   string // the config change, in the user's words
	Kept    string // what survives, or "" when nothing does

	// The fields the later steps need, which an address alone cannot give
	// (design §3.1). Retirements leaves them at their zero value; the
	// state's own attribute values fill them, through FillFromState.
	Logical   string // a secret container's logical name, "" otherwise
	SecretID  string // its Secret Manager ID, "" otherwise
	Job       string // a job's Cloud Run job name, "" otherwise
	Region    string // its region, "" otherwise
	Protected bool   // whether an ordinary apply can delete this address at all

	// Protected is NOT implied by the address alone: it is true for a
	// secret container (and for the registry, which carries prevent_destroy
	// too), and for a job whose state says deletion_protection = true. A job
	// whose protection is already false is one the apply deletes, and
	// state-removing it would leave it alive in GCP with nothing managing
	// it.
}

// KeptSecret is what a retired secret container leaves behind. The container
// is never destroyed: its module declares prevent_destroy, and the value it
// holds is not Terraform's to delete (design §4.2a).
const KeptSecret = "the secret and its value stay in Secret Manager; its container carries the fugaro labels, so re-adding it re-adopts it"

// KeptRegistry is the same promise for the repository's image registry: it
// is the module's second prevent_destroy resource (registry.tf), and nothing
// in a plan deletes an image.
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
		r.Kind = workflowKind(typ)
		// The variant itself is gone (design rows A and C) unless the spec
		// still builds it, in which case only this one resource of it
		// changed: an accessor to a secret it stopped mounting.
		if _, live := spec.Workflows[variant]; !live {
			r.Cause = variantCause(variant, spec)
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
// resource alive, while the optimistic one can.
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
			kind[i] = "job"
		}
	}
	if st == nil || st.Values == nil {
		return
	}
	at := make(map[string]int, len(rets))
	for i, r := range rets {
		at[r.Address] = i
	}
	// readProtection are the jobs whose state actually carried the
	// attribute; every other job counts as protected (see above).
	readProtection := make(map[int]bool)
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
			if fillFromValues(&rets[i], kind[i], res.Values) {
				readProtection[i] = true
			}
		}
		for _, c := range m.ChildModules {
			walk(c)
		}
	}
	walk(st.Values.RootModule)
	for i := range rets {
		if kind[i] == "job" && !readProtection[i] {
			rets[i].Protected = true
		}
	}
}

// fillFromValues reads one resource's own attributes: a job's name, location
// and deletion_protection, and a container's secret_id. The names are the
// provider schema's, and a value that is not there leaves its field empty.
// It reports whether the state carried deletion_protection at all.
func fillFromValues(r *Retirement, kind string, values json.RawMessage) (readProtection bool) {
	if len(values) == 0 {
		return false
	}
	var v struct {
		Name               *string `json:"name"`
		Location           *string `json:"location"`
		SecretID           *string `json:"secret_id"`
		DeletionProtection *bool   `json:"deletion_protection"`
	}
	if err := json.Unmarshal(values, &v); err != nil {
		return false
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
			return true
		}
	case "container":
		if v.SecretID != nil {
			r.SecretID = *v.SecretID
		}
	}
	return false
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
