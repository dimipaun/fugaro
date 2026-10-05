package tf

import (
	"fmt"
	"net/url"
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
	// Projects are the GCP projects of this run (the installation's first, then
	// a verified Firebase project): their own fugaro service accounts and
	// custom roles are Fugaro's.
	Projects []string
	// Listed are the IAM members the review screen listed (launchers,
	// operators, budget admins): the only people a plan may grant a role to.
	Listed []string
	// Registry is the Artifact Registry host of this run ("us-east5-docker.pkg.dev"),
	// Region the Cloud Run region, Buckets the run's own storage buckets (the
	// runs bucket) and Billing the billing account the review showed, if any.
	Registry, Region string
	Buckets          []string
	Billing          []string
}

// FugaroRoles are the custom role IDs the modules create; FugaroAccounts the
// fixed service account IDs. Job and build accounts are fugaro-<readable>-<8 hex>.
var (
	FugaroRoles    = []string{"fugaroLauncher", "fugaroJobRunner", "fugaroBuildSubmitter", "fugaroTagMover", "fugaroTokenMinter", "fugaroHistory"}
	FugaroAccounts = []string{"fugaro-scheduler", "fugaro-history", "fugaro-token-signer"}
)

var (
	saMember   = regexp.MustCompile(`^serviceAccount:([a-z][a-z0-9-]*)@([a-z0-9-]+)\.iam\.gserviceaccount\.com$`)
	jobAccount = regexp.MustCompile(`^fugaro-[a-z0-9-]+-[0-9a-f]{8}$`)
	repoID     = regexp.MustCompile(`^fugaro(-[a-z0-9-]+)?$`)
)

// knownCommands are the container command and args the modules set: none for
// a workflow's job, the history sweep and the image check.
var knownCommands = [][2][]string{
	{nil, nil},
	{{"/usr/local/bin/fugaro"}, {"budget", "history", "--sweep"}},
	{{"fugaro"}, {"image", "check", "--job"}},
}

// apiTargets are the two services the Firebase web key may call.
var apiTargets = []string{"identitytoolkit.googleapis.com", "securetoken.googleapis.com"}

// NotCovered lists why the plan is not covered, empty when it is. A plan that
// is nil, or that holds anything but creates and in-place updates of the
// module resources, whose attributes are not the modules', that grants a role
// outside the modules' roles, grants to someone who is not on the review screen
// or a Fugaro service account of this run, or widens a custom role, is not
// covered: its stage asks its own typed confirmation. It never allows: iam
// bindings, iam policies, audit configs, service account keys, an unknown
// resource type, allUsers and domain members.
func (c Cover) NotCovered(p *Plan) []string {
	if p == nil {
		return []string{"no plan"}
	}
	var out []string
	add := func(rc ResourceChange, why string) { out = append(out, rc.Address+" ("+why+")") }
	// What this plan creates, by module: a computed member or role is trusted
	// only when the resource it comes from is created in the same module.
	creates := map[string]bool{}
	for _, rc := range p.ResourceChanges {
		if slices.Contains(rc.Change.Actions, "create") {
			creates[modulePrefix(rc)+"|"+rc.Type] = true
		}
	}
	for _, rc := range p.ResourceChanges {
		acts := rc.Change.Actions
		switch {
		case len(acts) == 0:
			add(rc, "no actions")
			continue
		case !allOf(acts, "create", "update", "no-op", "read"):
			add(rc, "actions "+strings.Join(acts, ", "))
			continue
		}
		if allOf(acts, "no-op", "read") && rc.Change.Importing == nil {
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
		case rc.Change.Importing != nil && !c.importOK(rc.Change.Importing.ID):
			add(rc, "imports "+rc.Change.Importing.ID+", which is not in this run's projects")
		case c.attributes(rc, creates) != "":
			add(rc, c.attributes(rc, creates))
		case strings.HasSuffix(t, "_iam_member"):
			if why := c.checkGrant(rc.Change, creates[modulePrefix(rc)+"|google_service_account"], creates[modulePrefix(rc)+"|google_project_iam_custom_role"]); why != "" {
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
		if !createsSA { // a member computed from a service account this module creates
			return "a member that is not known and no service account is created with it"
		}
	default:
		m, _ := ch.After["member"].(string)
		switch {
		case m == "":
			return "no member"
		case strings.HasPrefix(m, "domain:"):
			return fmt.Sprintf("grants to %s, a whole domain", m)
		case slices.Contains(c.Listed, m):
			if !strings.HasPrefix(m, "user:") && !strings.HasPrefix(m, "group:") && !strings.HasPrefix(m, "serviceAccount:") {
				return fmt.Sprintf("grants to %s, which is not a user, group or service account", m)
			}
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
	sm := saMember.FindStringSubmatch(m)
	if sm == nil || !slices.Contains(c.Projects, sm[2]) {
		return false
	}
	return slices.Contains(FugaroAccounts, sm[1]) || jobAccount.MatchString(sm[1])
}

func (c Cover) fugaroRole(r string) bool {
	for _, p := range c.Projects {
		if id, ok := strings.CutPrefix(r, "projects/"+p+"/roles/"); ok && slices.Contains(FugaroRoles, id) {
			return true
		}
	}
	return false
}

// modulePrefix is the module path of a resource's address.
func modulePrefix(rc ResourceChange) string {
	if i := strings.LastIndex(rc.Address, "."+rc.Type+"."); i >= 0 {
		return rc.Address[:i]
	}
	return ""
}

// importOK: an import's ID names a resource of one of this run's projects.
func (c Cover) importOK(id string) bool {
	for _, p := range c.Projects {
		if strings.HasPrefix(id, p+"/") || strings.HasPrefix(id, "projects/"+p+"/") {
			return true
		}
	}
	return false
}

// at walks a plan value through blocks (lists of objects, of which the first is
// taken) and objects; a list of one object at the end reads as that object.
func at(v any, path ...string) any {
	for _, k := range path {
		if l, ok := v.([]any); ok {
			if len(l) == 0 {
				return nil
			}
			v = l[0]
		}
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	if l, ok := v.([]any); ok && len(l) == 1 {
		if _, isMap := l[0].(map[string]any); isMap {
			return l[0]
		}
	}
	return v
}

func str(v any) string { s, _ := v.(string); return s }

func strList(v any) []string {
	var out []string
	l, _ := v.([]any)
	for _, x := range l {
		out = append(out, str(x))
	}
	return out
}

// objects reads a block that may have collapsed to one object.
func objects(v any) []map[string]any {
	switch x := v.(type) {
	case map[string]any:
		return []map[string]any{x}
	case []any:
		var out []map[string]any
		for _, e := range x {
			if m, ok := e.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

// attributes checks what a covered resource points at: images in this run's
// registry, schedulers calling this run's jobs as Fugaro's accounts, a sink to
// this project, a private bucket, this run's projects and buckets, the
// billing account the review showed, and the web key's two API targets. ""
// when all is as the modules write it.
func (c Cover) attributes(rc ResourceChange, creates map[string]bool) string {
	a := rc.Change.After
	if a == nil {
		a = map[string]any{}
	}
	proj := ""
	if len(c.Projects) > 0 {
		proj = c.Projects[0]
	}
	pre := modulePrefix(rc)
	if p := str(a["project"]); p != "" && !slices.Contains(c.Projects, p) {
		return "in project " + p + ", which is not this run's"
	}
	if rc.Type == "google_storage_bucket_object" || (strings.HasPrefix(rc.Type, "google_storage_bucket_") && strings.HasSuffix(rc.Type, "_iam_member")) {
		switch b := str(a["bucket"]); {
		case b != "":
			if !slices.Contains(c.Buckets, b) {
				return "on bucket " + b + ", which is not this run's"
			}
		case !unknown(rc.Change, "bucket") || !creates[pre+"|google_storage_bucket"]:
			return "on a bucket that is not known"
		}
	}
	if strings.HasPrefix(rc.Type, "google_artifact_registry_repository_iam") {
		if r := str(a["repository"]); r != "" && !repoID.MatchString(r) {
			return "on repository " + r + ", which is not a Fugaro registry"
		}
	}
	switch rc.Type {
	case "google_cloud_run_v2_job":
		cs := objects(at(a["template"], "template", "containers"))
		if len(cs) == 0 {
			return "no container"
		}
		for _, m := range cs {
			if img := str(m["image"]); c.Registry == "" || !strings.HasPrefix(img, c.Registry+"/"+proj+"/") {
				return fmt.Sprintf("runs the image %q, outside this run's registry %s/%s/", img, c.Registry, proj)
			}
			cmd, args := strList(m["command"]), strList(m["args"])
			known := false
			for _, k := range knownCommands {
				known = known || (slices.Equal(cmd, k[0]) && slices.Equal(args, k[1]))
			}
			if !known {
				return "runs a command the modules do not set"
			}
		}
	case "google_cloud_scheduler_job":
		ht := at(a["http_target"])
		if at(rc.Change.AfterUnknown, "http_target", "uri") == true || unknown(rc.Change, "http_target") {
			// The job's name and location are known only once the job this
			// module creates exists.
			if !creates[pre+"|google_cloud_run_v2_job"] {
				return "calls a job that is not known and none is created with it"
			}
		} else {
			uri := str(at(ht, "uri"))
			u, err := url.Parse(uri)
			if err != nil || u.Scheme != "https" || u.Host != "run.googleapis.com" || !strings.HasPrefix(u.Path, "/v2/projects/"+proj+"/locations/"+c.Region+"/jobs/") || !strings.HasSuffix(u.Path, ":run") {
				return fmt.Sprintf("calls %q, not a job of this run's project and region on run.googleapis.com", uri)
			}
		}
		if at(rc.Change.AfterUnknown, "http_target", "oauth_token", "service_account_email") == true {
			if !creates[pre+"|google_service_account"] {
				return "calls as an account that is not known and none is created with it"
			}
		} else if email := str(at(ht, "oauth_token", "service_account_email")); email == "" || !c.fugaroAccount("serviceAccount:"+email) {
			return "calls as " + email + ", not a Fugaro service account of this run"
		}
	case "google_logging_project_sink":
		if unknown(rc.Change, "destination") {
			if !creates[pre+"|google_logging_project_bucket_config"] {
				return "sends logs to a place that is not known and no log bucket is created with it"
			}
		} else if d := str(a["destination"]); !strings.HasPrefix(d, "logging.googleapis.com/projects/"+proj+"/") {
			return fmt.Sprintf("sends logs to %q, not a log bucket of this project", d)
		}
	case "google_storage_bucket":
		if a["public_access_prevention"] != "enforced" || a["uniform_bucket_level_access"] != true {
			return "a bucket without public access prevention enforced and uniform access"
		}
	case "google_billing_budget":
		acct := strings.TrimPrefix(str(a["billing_account"]), "billingAccounts/")
		if acct == "" || !slices.Contains(c.Billing, acct) {
			return "a budget on billing account " + acct + ", which the review did not show"
		}
	case "google_apikeys_key":
		var got []string
		for _, t := range objects(at(a["restrictions"], "api_targets")) {
			got = append(got, str(t["service"]))
		}
		slices.Sort(got)
		if !slices.Equal(got, apiTargets) {
			return "a key whose API targets are not the web key's two"
		}
	}
	return ""
}
