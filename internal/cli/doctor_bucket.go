package cli

import (
	"context"
	"fmt"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// doctorRunsBucketIAM is the runs-bucket-iam check (docs/design/bucket-iam.md
// H9): who can write the runs bucket. Read-only; every failure to read is a
// warning, never an error. A test configuration (no_auth with no storage
// endpoint) is skipped, so no real bucket is ever read from one.
func doctorRunsBucketIAM(ctx context.Context, lc *localcfg.Config, c *crm.Service) []doctorCheck {
	if lc.Endpoints.NoAuth && lc.Endpoints.Storage == "" {
		return nil
	}
	bucket := lc.RunsBucketName()
	if bucket == "" {
		return nil
	}
	unchecked := func(err error) []doctorCheck {
		return []doctorCheck{{ID: "runs-bucket-iam", Severity: "warning",
			Problem: "could not check who can write gs://" + bucket + ": " + oneLineCLI(err.Error()),
			Fix:     "rerun fugaro doctor with access to the project, or as an operator"}, tokenGrantUnchecked(bucket)}
	}
	svc, err := storage.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.Storage)...)
	if err != nil {
		return unchecked(err)
	}
	launchers, operators := lc.Terraform.Launchers, lc.Terraform.Operators
	p, err := svc.Buckets.GetIamPolicy(bucket).OptionsRequestedPolicyVersion(3).Context(ctx).Do()
	switch {
	case err == nil:
		fs := infra.RunsBucketFindings(p, bucket, lc.GCPProject, launchers, operators)
		if len(launchers) > 0 && c != nil {
			req := &crm.GetIamPolicyRequest{Options: &crm.GetPolicyOptions{RequestedPolicyVersion: 3}}
			pp, perr := c.Projects.GetIamPolicy(lc.GCPProject, req).Context(ctx).Do()
			switch {
			case perr == nil:
				fs = append(fs, infra.ProjectStorageWriters(pp, launchers, operators)...)
			default:
				// A launcher's project-level role (roles/editor, say) bypasses
				// the bucket's own policy entirely: failing to read it must
				// not silently leave the summary OK, as if nothing bypasses it.
				fs = append(fs, infra.BucketFinding{Severity: "warning",
					Problem: "could not check which launchers hold project-level storage roles (which bypass the bucket's own policy): " + oneLineCLI(perr.Error()),
					Fix:     "rerun fugaro doctor with access to the project, or as an operator"})
			}
		}
		return append(findingChecks(fs), tokenGrantChecks(infra.JobTokenGrantFindings(p, bucket, lc.GCPProject))...)
	case isAccessDenied(err):
		r, terr := svc.Buckets.TestIamPermissions(bucket, []string{"storage.objects.create", "storage.objects.delete"}).Context(ctx).Do()
		if terr != nil {
			return unchecked(terr)
		}
		return []doctorCheck{selfCheck(bucket, len(r.Permissions) > 0, lc.User, launchers, operators), tokenGrantUnchecked(bucket)}
	default:
		return unchecked(err)
	}
}

// selfCheck places the caller (only by the local config's self-asserted
// user) and says what its unconditioned write access means. Which list
// places it is read off the same two predicates every other command that
// asks "is this identity an operator of the installation" reads (roles.go).
func selfCheck(bucket string, held bool, user string, launchers, operators []string) doctorCheck {
	if !held {
		return doctorCheck{ID: "runs-bucket-iam", OK: true}
	}
	m := meMember(user)
	const text = "your credentials can write anywhere in gs://%s: this installation predates the 0.7.0 bucket hardening"
	switch {
	case operatorMember(m, operators):
		return doctorCheck{ID: "runs-bucket-iam", OK: true}
	case launcherMember(m, launchers):
		return doctorCheck{ID: "runs-bucket-iam", Severity: "warning", Problem: fmt.Sprintf(text, bucket),
			Fix: "ask an operator to run fugaro setup (0.7.0 or later)"}
	default:
		return doctorCheck{ID: "runs-bucket-iam", Severity: "info", Problem: "expected for an operator; otherwise " + fmt.Sprintf(text, bucket),
			Fix: "if you only launch runs, ask an operator to run fugaro setup (0.7.0 or later)"}
	}
}

// tokenGrantUnchecked is job-token-grant when the bucket's policy cannot be
// read (a launcher's usual case): never a silent pass, never a failure.
func tokenGrantUnchecked(bucket string) doctorCheck {
	return doctorCheck{ID: "job-token-grant", Severity: "info", unread: true,
		Problem: "unchecked: cannot read the bucket policy of gs://" + bucket + ", so whether each repository's job account can read its budget token objects under tokens/ is not known",
		Fix:     "an operator runs fugaro doctor, which can read it"}
}

// tokenGrantChecks are JobTokenGrantFindings as doctor checks: the
// job-token-grant summary, a warning when any job account lacks its tokens/
// grant, then one job-token-grant-N line per account.
func tokenGrantChecks(fs []infra.BucketFinding) []doctorCheck {
	sum := doctorCheck{ID: "job-token-grant", OK: true}
	if len(fs) > 0 {
		sum.OK, sum.Severity = false, "info"
		sum.Problem = "a repository's job account could not be checked for its read grant on tokens/ (see the job-token-grant-N lines)"
		sum.Fix = "an operator runs fugaro update --repo in each repository named"
		for _, f := range fs {
			if f.Severity == "warning" {
				sum.Severity = "warning"
				sum.Problem = "a repository's job account cannot read its budget token objects under tokens/ (see the job-token-grant-N lines)"
			}
		}
	}
	out := []doctorCheck{sum}
	for i, f := range fs {
		out = append(out, doctorCheck{ID: fmt.Sprintf("job-token-grant-%d", i+1), Severity: f.Severity,
			Problem: pluginwire.Printable(f.Problem), Fix: pluginwire.Printable(f.Fix)})
	}
	return out
}

// findingChecks are the findings as doctor checks: one per finding, IDs
// runs-bucket-iam-1.., plus the runs-bucket-iam summary, OK when no finding
// is a warning.
func findingChecks(fs []infra.BucketFinding) []doctorCheck {
	sum := doctorCheck{ID: "runs-bucket-iam", OK: true}
	var out []doctorCheck
	for i, f := range fs {
		if f.Severity == "warning" {
			sum.OK, sum.Severity, sum.Problem = false, "warning", "someone other than the operators can write fugaro/ (see the runs-bucket-iam-N lines)"
		}
		out = append(out, doctorCheck{ID: fmt.Sprintf("runs-bucket-iam-%d", i+1), Severity: f.Severity,
			// Fix carries the raw member (via shellword.Quote) so the
			// printed gcloud command is actually correct to paste; Printable
			// only escapes it for safe terminal display, same as Problem.
			Problem: pluginwire.Printable(f.Problem), Fix: pluginwire.Printable(f.Fix)})
	}
	return append([]doctorCheck{sum}, out...)
}
