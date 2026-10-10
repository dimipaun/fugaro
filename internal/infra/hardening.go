package infra

import (
	"regexp"
	"slices"
	"strings"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/shellword"
)

// BucketFinding is one thing doctor reports about who can write the runs
// bucket (docs/design/bucket-iam.md H9).
type BucketFinding struct{ Severity, Member, Role, Problem, Fix string }

// bucketWriteRoles are the predefined roles that write objects.
var bucketWriteRoles = []string{"roles/storage.objectAdmin", "roles/storage.objectUser", "roles/storage.objectCreator",
	"roles/storage.legacyBucketWriter", "roles/storage.legacyBucketOwner", "roles/storage.admin"}

// projectWriteRoles are the project-level roles that write every bucket's objects.
var projectWriteRoles = []string{"roles/owner", "roles/editor", "roles/storage.admin", "roles/storage.objectAdmin",
	"roles/storage.objectUser", "roles/storage.objectCreator"}

func customRole(r string) bool {
	return strings.HasPrefix(r, "projects/") || strings.HasPrefix(r, "organizations/")
}

// fugaroAccountRE is a job or build account's shape, pinned to project: a
// member from another project that merely looks like one (same
// "fugaro-<slug>" prefix) must never be silently exempted, since it is not
// an account this installation's own Terraform could have created.
func fugaroAccountRE(project string) *regexp.Regexp {
	return regexp.MustCompile(`^serviceAccount:fugaro-[a-z0-9-]+@` + regexp.QuoteMeta(project) + `\.iam\.gserviceaccount\.com$`)
}

// broadWriteRoles are the two roles that write every object in the bucket
// unconditionally, the ones a doctor whose lists are known should never
// see an unrecognized service account hold.
var broadWriteRoles = []string{"roles/storage.objectAdmin", "roles/storage.admin"}

// RunsBucketFindings reads the runs bucket's policy against the 0.7.0 end
// state. With launchers and operators unknown (both empty), an unconditioned
// writer is info, never a warning. project pins which service accounts this
// installation's own Terraform could have created: a same-shaped account
// from another project is a stranger, not a job or build account.
func RunsBucketFindings(p *storage.Policy, bucket, project string, launchers, operators []string) []BucketFinding {
	known := len(launchers)+len(operators) > 0
	fugaroAccountMember := fugaroAccountRE(project)
	launcherOnly := func(m string) bool { return slices.Contains(launchers, m) && !slices.Contains(operators, m) }
	removeCmd := func(m, r string) string {
		return "gcloud storage buckets remove-iam-policy-binding gs://" + bucket + " --member=" + shellword.Quote(m) + " --role=" + r
	}
	var out []BucketFinding
	reader, writer := map[string]bool{}, map[string]bool{}
	conventionNoted := false
	for _, bd := range p.Bindings {
		write := slices.Contains(bucketWriteRoles, bd.Role) || customRole(bd.Role)
		for _, m := range bd.Members {
			pm := pluginwire.Printable(m)
			switch {
			case bd.Role == "roles/storage.objectViewer" && bd.Condition == nil:
				reader[m] = true
			case !write:
			case strings.HasPrefix(m, "projectOwner:") || strings.HasPrefix(m, "projectEditor:"):
				if !conventionNoted {
					conventionNoted = true
					out = append(out, BucketFinding{Severity: "info", Member: pm, Role: bd.Role,
						Problem: "project Owners and Editors can write fugaro/ (GCS's convenience binding, and their project roles): treat them as operators"})
				}
			case bd.Condition != nil && bd.Role == "roles/storage.objectUser" && bd.Condition.Title == gcp.LauncherBucketConditionTitle &&
				bd.Condition.Expression == gcp.LauncherBucketCondition(bucket) && bd.Condition.Description == "":
				writer[m] = true
			case bd.Condition != nil && fugaroAccountMember.MatchString(m) && bd.Role == "roles/storage.objectUser":
				// A job or build account under its own prefixes (the guard checks the shape).
			case bd.Condition != nil:
				out = append(out, BucketFinding{Severity: "warning", Member: pm, Role: bd.Role,
					Problem: pm + " holds " + bd.Role + " on gs://" + bucket + " under a condition that is not the launchers' runs/ condition",
					Fix:     removeCmd(m, bd.Role) + " --all"})
			case slices.Contains(operators, m) && bd.Role == "roles/storage.objectAdmin":
			case customRole(bd.Role):
				out = append(out, BucketFinding{Severity: "warning", Member: pm, Role: bd.Role,
					Problem: pm + " holds the custom role " + bd.Role + " on gs://" + bucket + ": doctor cannot tell whether it writes fugaro/",
					Fix:     "review the role, or " + removeCmd(m, bd.Role)})
			case known && strings.HasPrefix(m, "serviceAccount:") && slices.Contains(broadWriteRoles, bd.Role):
				out = append(out, BucketFinding{Severity: "warning", Member: pm, Role: bd.Role,
					Problem: pm + " is an unrecognized service account holding " + bd.Role + " on gs://" + bucket + " unconditionally: expected only for an operator, or a job/build account under its own condition",
					Fix:     removeCmd(m, bd.Role)})
			case !known || strings.HasPrefix(m, "serviceAccount:"):
				out = append(out, BucketFinding{Severity: "info", Member: pm, Role: bd.Role,
					Problem: pm + " can write anywhere in gs://" + bucket + " (" + bd.Role + "): expected only for an operator"})
			case launcherOnly(m):
				out = append(out, BucketFinding{Severity: "warning", Member: pm, Role: bd.Role,
					Problem: "launcher " + pm + " can write anywhere in gs://" + bucket + " (" + bd.Role + "): the installation predates the 0.7.0 bucket hardening",
					Fix:     "an operator runs fugaro init (0.7.0 or later), which replaces the grant"})
			default:
				out = append(out, BucketFinding{Severity: "warning", Member: pm, Role: bd.Role,
					Problem: pm + " is neither a launcher nor an operator and can write anywhere in gs://" + bucket + " (" + bd.Role + ")",
					Fix:     removeCmd(m, bd.Role)})
			}
		}
	}
	for _, m := range launchers {
		if launcherOnly(m) && (!reader[m] || !writer[m]) {
			out = append(out, BucketFinding{Severity: "warning", Member: pluginwire.Printable(m),
				Problem: "launcher " + pluginwire.Printable(m) + " lacks the 0.7.0 runs bucket grants (read everything, write runs/)",
				Fix:     "an operator runs fugaro init (0.7.0 or later)"})
		}
	}
	return out
}

// ProjectStorageWriters lists the launchers (not operators) whose project
// roles write every bucket, which the bucket's policy cannot bind (H12).
// Groups are not expanded.
func ProjectStorageWriters(p *crm.Policy, launchers, operators []string) []BucketFinding {
	var out []BucketFinding
	for _, bd := range p.Bindings {
		if !slices.Contains(projectWriteRoles, bd.Role) {
			continue
		}
		for _, m := range bd.Members {
			if slices.Contains(launchers, m) && !slices.Contains(operators, m) {
				pm := pluginwire.Printable(m)
				out = append(out, BucketFinding{Severity: "warning", Member: m, Role: bd.Role,
					Problem: "launcher " + pm + " holds " + bd.Role + " on the project, which writes fugaro/ whatever the bucket's policy says",
					Fix:     "make " + pm + " an operator (fugaro init --operator), or remove the project role"})
			}
		}
	}
	return out
}
