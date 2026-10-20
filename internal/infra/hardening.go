package infra

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/shellword"
)

// BucketFinding is one thing doctor reports about who can write the runs
// bucket (docs/design/bucket-iam.md H9).
type BucketFinding struct{ Severity, Member, Role, Problem, Fix string }

// sealRelease is the release whose launchers write the sealed budget token
// under tokens/, and so need the widened launcher condition
// (gcp.LauncherBucketCondition): an installation initialised before it has
// the runs/-only one. It names the release the seal ships in.
const sealRelease = "0.9.0"

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
			case bd.Condition != nil && bd.Role == "roles/storage.objectUser" && bd.Condition.Title == gcp.LegacyLauncherBucketConditionTitle &&
				bd.Condition.Expression == gcp.LegacyLauncherBucketCondition(bucket) && bd.Condition.Description == "":
				// The launchers' own grant in its pre-0.9.0 form, on an
				// installation not re-initialised since the budget token moved
				// to tokens/. Not writer[m]: the run this member starts cannot
				// write its sealed token, and the loop below says so from the
				// launchers list. The fix is the init that replaces the
				// binding, never a removal — the member holds the runs/ write
				// it is supposed to hold, and taking it away would break
				// launching (this case used to fall through to the catch-all
				// below, which prints remove-iam-policy-binding --all).
				out = append(out, BucketFinding{Severity: "warning", Member: pm, Role: bd.Role,
					Problem: pm + " holds " + bd.Role + " on gs://" + bucket + " under the launchers' pre-0.9.0 runs/-only condition (titled " + gcp.LegacyLauncherBucketConditionTitle + "): the installation has not been re-initialised since the budget token moved to tokens/, so its launchers cannot write the sealed budget token",
					Fix:     "an operator runs fugaro init (" + sealRelease + " or later), which replaces the grant"})
			case bd.Condition != nil && fugaroAccountMember.MatchString(m) && bd.Role == "roles/storage.objectUser":
				// A job or build account under its own prefixes (the guard checks the shape).
			case bd.Condition != nil:
				out = append(out, BucketFinding{Severity: "warning", Member: pm, Role: bd.Role,
					Problem: pm + " holds " + bd.Role + " on gs://" + bucket + " under a condition that is not the launchers' runs/ and tokens/ condition",
					Fix:     removeCmd(m, bd.Role) + " --all"})
			case slices.Contains(operators, m) && bd.Role == "roles/storage.objectAdmin":
			case customRole(bd.Role):
				out = append(out, BucketFinding{Severity: "warning", Member: pm, Role: bd.Role,
					Problem: pm + " holds the custom role " + bd.Role + " on gs://" + bucket + ": doctor cannot tell whether it writes fugaro/",
					Fix:     "review the role, or " + removeCmd(m, bd.Role)})
			case known && strings.HasPrefix(m, "serviceAccount:"):
				// Every role reaching this point is already a write role
				// (bucketWriteRoles, checked by `write` above): objectAdmin
				// and storage.admin are not the only ones that write every
				// object, so this is not restricted to those two (the
				// review finding this fixed: objectUser, objectCreator,
				// legacyBucketWriter and legacyBucketOwner were previously
				// left at "info" here, even with known lists).
				out = append(out, BucketFinding{Severity: "warning", Member: pm, Role: bd.Role,
					Problem: pm + " is an unrecognized service account holding " + bd.Role + " on gs://" + bucket + " unconditionally: expected only for an operator, or a job/build account under its own condition",
					Fix:     removeCmd(m, bd.Role)})
			case !known:
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
				Problem: "launcher " + pluginwire.Printable(m) + " lacks the runs bucket grants (read everything, write runs/ and tokens/: the 0.7.0 hardening, widened to tokens/ when the budget token moved there)",
				Fix:     "an operator runs fugaro init (" + sealRelease + " or later)"})
		}
	}
	return out
}

// jobRunsSlugRE takes the slug out of a job account's runs/ condition.
var jobRunsSlugRE = regexp.MustCompile(`/objects/runs/([a-z0-9][a-z0-9_-]*)/"\)`)

// JobTokenGrantFindings reports each job account of this installation that
// holds its objectUser grant on its repository's runs/, cache/ and locks/
// (gcp.BucketCondition with gcp.JobBucketPrefixes, titled
// gcp.BucketConditionTitle) but not the read-only grant on that repository's
// tokens/ (gcp.TokenBucketCondition, titled gcp.TokenBucketConditionTitle,
// roles/storage.objectViewer): a repository not re-applied since the sealed
// budget token moved to tokens/ (docs/design/budget-token-channel.md §7.2).
// Its runs cannot read their token, and GCS answers the refused read like a
// missing object, so the run alone cannot say which. Read from the bucket's
// own policy: the same read runs-bucket-iam makes, nothing more.
//
// A shared project job account is judged the same way and against its own
// pair: its objectUser grant is the widened gcp.ProjectBucketCondition, and
// the read it must hold is the whole-tokens/ gcp.ProjectTokenBucketCondition
// titled gcp.ProjectTokenBucketConditionTitle (plan task 3.2 step 2,
// budget-token-channel.md §7.2: the shared job reads every repository's
// sealed token, and the seal is what keeps that from being a token).
func JobTokenGrantFindings(p *storage.Policy, bucket, project string) []BucketFinding {
	account := fugaroAccountRE(project)
	type job struct {
		member, saID, slug string
		project            bool // a shared project job account
	}
	var jobs []job
	var odd []BucketFinding
	has := map[string]bool{}
	for _, bd := range p.Bindings {
		if bd.Condition == nil || bd.Condition.Description != "" {
			continue
		}
		for _, m := range bd.Members {
			if !account.MatchString(m) {
				continue
			}
			switch bd.Role {
			case "roles/storage.objectUser":
				if !strings.Contains(bd.Condition.Expression, "/objects/runs/") {
					continue // a build account (builds/): it reads no token
				}
				if bd.Condition.Expression == gcp.ProjectBucketCondition(bucket, gcp.JobBucketPrefixes) {
					// The shared job's widened grant (phase 3): the account
					// serves every repository of its profile, so the grant it
					// must hold is the whole-tokens/ one.
					saID, ok := strings.CutPrefix(bd.Condition.Title, gcp.BucketConditionTitlePrefix)
					if !ok || saID == "" {
						pm := pluginwire.Printable(m)
						odd = append(odd, BucketFinding{Severity: "info", Member: pm, Role: bd.Role,
							Problem: "project job account " + pm + ": tokens/ read grant not checked (its runs/ grant on gs://" + bucket + " is the widened condition, but its title is not " + gcp.BucketConditionTitlePrefix + "<account>)",
							Fix:     "an operator runs fugaro init (" + sealRelease + " or later), which writes both grants"})
						continue
					}
					jobs = append(jobs, job{member: m, saID: saID, project: true})
					continue
				}
				saID, ok := strings.CutPrefix(bd.Condition.Title, gcp.BucketConditionTitlePrefix)
				sm := jobRunsSlugRE.FindStringSubmatch(bd.Condition.Expression)
				if !ok || sm == nil || bd.Condition.Expression != gcp.BucketCondition(bucket, gcp.JobBucketPrefixes, sm[1]) {
					// Not the shape this fugaro writes, so the slug and the
					// expected tokens/ grant cannot be derived: said, never
					// passed over as fine.
					pm := pluginwire.Printable(m)
					odd = append(odd, BucketFinding{Severity: "info", Member: pm, Role: bd.Role,
						Problem: "job account " + pm + ": tokens/ read grant not checked (its runs/ grant on gs://" + bucket + " is not in the shape this fugaro writes)",
						Fix:     "an operator runs fugaro init --repo (" + sealRelease + " or later) in that repository's checkout, which writes both grants"})
					continue
				}
				jobs = append(jobs, job{member: m, saID: saID, slug: sm[1]})
			case "roles/storage.objectViewer":
				has[m+"\x00"+bd.Condition.Title+"\x00"+bd.Condition.Expression] = true
			}
		}
	}
	out := odd
	for _, j := range jobs {
		pm := pluginwire.Printable(j.member)
		if j.project {
			if has[j.member+"\x00"+gcp.ProjectTokenBucketConditionTitle(j.saID)+"\x00"+gcp.ProjectTokenBucketCondition(bucket)] {
				continue
			}
			out = append(out, BucketFinding{Severity: "warning", Member: pm, Role: "roles/storage.objectViewer",
				Problem: "project job account " + pm + " cannot read the bucket's budget token objects (gs://" + bucket + "/tokens/): the installation has not been re-applied since the sealed budget token moved to tokens/, so every run of its job that carries a budget token stops at bootstrap, reading the token as missing",
				Fix:     "an operator runs fugaro init (" + sealRelease + " or later)"})
			continue
		}
		if has[j.member+"\x00"+gcp.TokenBucketConditionTitle(j.saID)+"\x00"+gcp.TokenBucketCondition(bucket, j.slug)] {
			continue
		}
		out = append(out, BucketFinding{Severity: "warning", Member: pm, Role: "roles/storage.objectViewer",
			Problem: "job account " + pm + " cannot read its repository's budget token objects (gs://" + bucket + "/tokens/" + j.slug + "/): the repository has not been re-applied since the sealed budget token moved to tokens/, so its runs with a budget token stop at bootstrap, reading the token as missing",
			Fix:     "an operator runs fugaro init --repo (" + sealRelease + " or later) in that repository's checkout"})
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

const bucketGrantPrefix = "module.installation.google_storage_bucket_iam_member."

// grantMember is the member of address name["<member>"], ok false for any
// other address.
func grantMember(address, name string) (string, bool) {
	rest, ok := strings.CutPrefix(address, bucketGrantPrefix+name+"[")
	if !ok || !strings.HasSuffix(rest, "]") {
		return "", false
	}
	m, err := strconv.Unquote(strings.TrimSuffix(rest, "]"))
	return m, err == nil
}

// HardeningAllowDelete is the 0.7.0 bucket hardening's allow-list for p
// (docs/design/bucket-iam.md H7): each delete of a launcher's old objectAdmin
// grant on the runs bucket, runs["<m>"], where m is a launcher and not an
// operator and p also creates m's runs_reader and runs_launcher grants.
// A replace, another role, another resource, an operator's grant or a
// member on neither list is never allowed: the guard refuses it as before.
func HardeningAllowDelete(p *tf.Plan, launchers, operators []string) []string {
	if p == nil {
		return nil
	}
	created := map[string]bool{}
	for _, rc := range p.ResourceChanges {
		if slices.Equal(rc.Change.Actions, []string{"create"}) {
			created[rc.Address] = true
		}
	}
	var out []string
	for _, rc := range p.ResourceChanges {
		m, ok := grantMember(rc.Address, "runs")
		if !ok || !slices.Equal(rc.Change.Actions, []string{"delete"}) {
			continue
		}
		if role, _ := rc.Change.Before["role"].(string); role != "roles/storage.objectAdmin" {
			continue
		}
		if !slices.Contains(launchers, m) || slices.Contains(operators, m) {
			continue
		}
		q := strconv.Quote(m)
		if !created[bucketGrantPrefix+"runs_reader["+q+"]"] || !created[bucketGrantPrefix+"runs_launcher["+q+"]"] {
			continue
		}
		out = append(out, rc.Address)
	}
	slices.Sort(out)
	return out
}

// LauncherGrantUpgrade is the 0.9.0 upgrade's allow-list for p
// (docs/design/bucket-iam.md H5): each replace of a launcher's
// runs_launcher["<m>"] grant on bucket whose before is the pre-0.9.0 grant
// (objectUser under gcp.LegacyLauncherBucketCondition and its title, no
// description) and whose after is the same member's objectUser grant on the
// same bucket under gcp.LauncherBucketCondition and its title. The widened
// condition is ForceNew, so every installation set up before 0.9.0 plans
// this replace once per launcher who is not an operator. Anything else — a
// pure delete, another before or after, another bucket, role or member, an
// operator, a member on no list — is never allowed: the guard refuses it as
// before.
func LauncherGrantUpgrade(p *tf.Plan, bucket string, launchers, operators []string) []string {
	if p == nil || !gcp.ValidBucketName(bucket) {
		return nil
	}
	const role = "roles/storage.objectUser"
	var out []string
	for _, rc := range p.ResourceChanges {
		m, ok := grantMember(rc.Address, "runs_launcher")
		if !ok || rc.Type != "google_storage_bucket_iam_member" {
			continue
		}
		a := rc.Change.Actions
		if !slices.Equal(a, []string{"delete", "create"}) && !slices.Equal(a, []string{"create", "delete"}) {
			continue
		}
		if !slices.Contains(launchers, m) || slices.Contains(operators, m) {
			continue
		}
		if !launcherGrantIs(rc.Change.Before, bucket, m, role, gcp.LegacyLauncherBucketConditionTitle, gcp.LegacyLauncherBucketCondition(bucket)) ||
			!launcherGrantIs(rc.Change.After, bucket, m, role, gcp.LauncherBucketConditionTitle, gcp.LauncherBucketCondition(bucket)) {
			continue
		}
		out = append(out, rc.Address)
	}
	slices.Sort(out)
	return out
}

// launcherGrantIs reports whether one side of a google_storage_bucket_iam_member
// change is exactly member's role on bucket under the one condition titled
// title with expression expr and no description.
func launcherGrantIs(side map[string]any, bucket, member, role, title, expr string) bool {
	if side == nil || side["bucket"] != bucket || side["member"] != member || side["role"] != role {
		return false
	}
	conds, _ := side["condition"].([]any)
	if len(conds) != 1 {
		return false
	}
	c, _ := conds[0].(map[string]any)
	if c == nil || c["title"] != title || c["expression"] != expr {
		return false
	}
	d, set := c["description"]
	return !set || d == nil || d == ""
}

// LauncherGrantUpgradeBanner names the launchers whose runs_launcher grant
// the plan replaces with the runs/ and tokens/ condition, "" when there are
// none.
func LauncherGrantUpgradeBanner(allowed []string) string {
	if len(allowed) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("bucket access (0.9.0): these launchers' runs/ grant is replaced by one on runs/ and tokens/, so the runs they start can write their sealed budget token:\n")
	for _, a := range allowed {
		m, _ := grantMember(a, "runs_launcher")
		b.WriteString("  " + pluginwire.Printable(m) + "\n")
	}
	return b.String()
}

// HardeningBanner names the launchers whose old grant the plan deletes, ""
// when there are none.
func HardeningBanner(allowed []string) string {
	if len(allowed) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("bucket access (0.7.0): these launchers keep reading the runs bucket and writing runs/, and lose write access to fugaro/, builds/, cache/ and locks/:\n")
	for _, a := range allowed {
		m, _ := grantMember(a, "runs")
		b.WriteString("  " + pluginwire.Printable(m) + "\n")
	}
	return b.String()
}
