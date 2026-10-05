package tf

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// allowedTypes are the resource types our embedded modules declare
// (deploy/terraform/gcp). A plan is covered by init's one confirmation only if
// every resource in it is one of them: TestAllowedTypesCoverTheModules fails
// when a module adds a type that is not listed here, so adding one is a
// decision, not an accident.
var allowedTypes = map[string]bool{
	"google_apikeys_key":                             true,
	"google_artifact_registry_repository":            true,
	"google_artifact_registry_repository_iam_member": true,
	"google_billing_budget":                          true,
	"google_cloud_run_v2_job":                        true,
	"google_cloud_run_v2_job_iam_member":             true,
	"google_cloud_scheduler_job":                     true,
	"google_firebase_database_instance":              true,
	"google_firebase_project":                        true,
	"google_logging_log_view":                        true,
	"google_logging_log_view_iam_member":             true,
	"google_logging_project_bucket_config":           true,
	"google_logging_project_exclusion":               true,
	"google_logging_project_sink":                    true,
	"google_monitoring_alert_policy":                 true,
	"google_monitoring_notification_channel":         true,
	"google_project_iam_custom_role":                 true,
	"google_project_iam_member":                      true,
	"google_project_service":                         true,
	"google_secret_manager_secret":                   true,
	"google_secret_manager_secret_iam_member":        true,
	"google_service_account":                         true,
	"google_service_account_iam_member":              true,
	"google_storage_bucket":                          true,
	"google_storage_bucket_iam_member":               true,
	"google_storage_bucket_object":                   true,
}

// allowedRoles are the predefined roles the modules grant. A custom role of
// this installation (projects/<p>/roles/fugaro...) is allowed besides them.
// roles/iam.serviceAccountUser is only ever granted to a principal the check
// already accepts (a Fugaro service account or a listed member).
var allowedRoles = map[string]bool{
	"roles/aiplatform.user":                   true,
	"roles/artifactregistry.reader":           true,
	"roles/artifactregistry.writer":           true,
	"roles/datastore.user":                    true,
	"roles/datastore.viewer":                  true,
	"roles/firebaseauth.admin":                true,
	"roles/firebasedatabase.admin":            true,
	"roles/firebasedatabase.viewer":           true,
	"roles/iam.serviceAccountUser":            true,
	"roles/logging.logWriter":                 true,
	"roles/logging.viewAccessor":              true,
	"roles/run.invoker":                       true,
	"roles/secretmanager.secretAccessor":      true,
	"roles/secretmanager.secretVersionAdder":  true,
	"roles/secretmanager.viewer":              true,
	"roles/serviceusage.serviceUsageConsumer": true,
	"roles/storage.objectAdmin":               true,
	"roles/storage.objectUser":                true,
	"roles/storage.objectViewer":              true,
}

// Cover says which plans init's one confirmation may cover.
type Cover struct {
	// Projects are the GCP projects of this run (the installation's, and the
	// Firebase project's): their own fugaro-* service accounts and custom
	// roles are Fugaro's.
	Projects []string
	// Listed are the IAM members the review screen listed (launchers,
	// operators, budget admins): the only people a plan may grant a role to.
	Listed []string
}

var fugaroSA = regexp.MustCompile(`^serviceAccount:fugaro[a-z0-9-]*@([a-z0-9-]+)\.iam\.gserviceaccount\.com$`)

// NotCovered lists why the plan is not covered, empty when it is. A plan that
// is nil, or that holds anything but creates and in-place updates of the
// module resources, grants a role outside the modules' roles, grants to
// someone who is not on the review screen or a Fugaro service account of this
// run, or widens a custom role, is not covered: its stage asks its own typed
// confirmation. It never allows: iam bindings, iam policies, audit configs,
// service account keys, an unknown resource type, allUsers and domain members.
func (c Cover) NotCovered(p *Plan) []string {
	if p == nil {
		return []string{"no plan"}
	}
	var out []string
	add := func(rc ResourceChange, why string) { out = append(out, rc.Address+" ("+why+")") }
	createsSA, createsRole := false, false
	for _, rc := range p.ResourceChanges {
		if slices.Contains(rc.Change.Actions, "create") {
			createsSA = createsSA || rc.Type == "google_service_account"
			createsRole = createsRole || rc.Type == "google_project_iam_custom_role"
		}
	}
	for _, rc := range p.ResourceChanges {
		acts := rc.Change.Actions
		switch {
		case len(acts) == 0:
			add(rc, "no actions")
			continue
		case !c.onlyAdditive(acts):
			add(rc, "actions "+strings.Join(acts, ", "))
			continue
		}
		if allOf(acts, "no-op", "read") {
			continue
		}
		t := rc.Type
		switch {
		case strings.HasSuffix(t, "_iam_binding") || strings.HasSuffix(t, "_iam_policy") || strings.HasSuffix(t, "_iam_audit_config"):
			add(rc, "an authoritative IAM resource")
		case t == "google_service_account_key":
			add(rc, "a service account key")
		case !allowedTypes[t]:
			add(rc, "a resource type outside the installation's modules")
		case t == "google_project_iam_custom_role" && slices.Contains(acts, "update") && !samePermissions(rc.Change):
			add(rc, "changes a custom role's permissions")
		case strings.HasSuffix(t, "_iam_member"):
			if why := c.checkGrant(rc.Change, createsSA, createsRole); why != "" {
				add(rc, why)
			}
		}
	}
	return out
}

func allOf(acts []string, ok ...string) bool {
	for _, a := range acts {
		if !slices.Contains(ok, a) {
			return false
		}
	}
	return true
}

func (Cover) onlyAdditive(acts []string) bool {
	return allOf(acts, "create", "update", "no-op", "read")
}

func samePermissions(c Change) bool {
	b, ok1 := c.Before["permissions"].([]any)
	a, ok2 := c.After["permissions"].([]any)
	if !ok1 || !ok2 {
		return false
	}
	if m, ok := c.AfterUnknown.(map[string]any); ok && m["permissions"] == true {
		return false
	}
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		if !slices.Contains(b, x) {
			return false
		}
	}
	return true
}

// unknown reports whether the plan knows the attribute only after apply.
func unknown(c Change, attr string) bool {
	m, ok := c.AfterUnknown.(map[string]any)
	return ok && m[attr] == true
}

func (c Cover) checkGrant(ch Change, createsSA, createsRole bool) string {
	// The principal.
	switch {
	case unknown(ch, "member"):
		if !createsSA { // a member computed from a service account this plan creates
			return "a member that is not known and no service account is created with it"
		}
	default:
		m, _ := ch.After["member"].(string)
		switch {
		case m == "":
			return "no member"
		case slices.Contains(c.Listed, m):
		case c.fugaroAccount(m):
		default:
			return fmt.Sprintf("grants to %s, who is not on the review screen", m)
		}
	}
	// The role.
	if unknown(ch, "role") {
		if !createsRole {
			return "a role that is not known and no custom role is created with it"
		}
		return ""
	}
	role, _ := ch.After["role"].(string)
	if allowedRoles[role] || c.fugaroRole(role) {
		return ""
	}
	return fmt.Sprintf("grants the role %q, which the installation's modules do not use", role)
}

func (c Cover) fugaroAccount(m string) bool {
	sm := fugaroSA.FindStringSubmatch(m)
	return sm != nil && slices.Contains(c.Projects, sm[1])
}

func (c Cover) fugaroRole(r string) bool {
	for _, p := range c.Projects {
		if rest, ok := strings.CutPrefix(r, "projects/"+p+"/roles/fugaro"); ok && !strings.ContainsAny(rest, "/ ") {
			return true
		}
	}
	return false
}
