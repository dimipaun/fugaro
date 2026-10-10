package cli

import (
	"context"
	"fmt"
	"slices"

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
			Fix:     "rerun fugaro doctor with access to the project, or as an operator"}}
	}
	svc, err := storage.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.Storage)...)
	if err != nil {
		return unchecked(err)
	}
	launchers, operators := lc.Terraform.Launchers, lc.Terraform.Operators
	p, err := svc.Buckets.GetIamPolicy(bucket).OptionsRequestedPolicyVersion(3).Context(ctx).Do()
	switch {
	case err == nil:
		fs := infra.RunsBucketFindings(p, bucket, launchers, operators)
		if len(launchers) > 0 && c != nil {
			req := &crm.GetIamPolicyRequest{Options: &crm.GetPolicyOptions{RequestedPolicyVersion: 3}}
			if pp, perr := c.Projects.GetIamPolicy(lc.GCPProject, req).Context(ctx).Do(); perr == nil {
				fs = append(fs, infra.ProjectStorageWriters(pp, launchers, operators)...)
			}
		}
		return findingChecks(fs)
	case isAccessDenied(err):
		r, terr := svc.Buckets.TestIamPermissions(bucket, []string{"storage.objects.create", "storage.objects.delete"}).Context(ctx).Do()
		if terr != nil {
			return unchecked(terr)
		}
		return []doctorCheck{selfCheck(bucket, len(r.Permissions) > 0, lc.User, launchers, operators)}
	default:
		return unchecked(err)
	}
}

// selfCheck places the caller (only by the local config's self-asserted
// user) and says what its unconditioned write access means.
func selfCheck(bucket string, held bool, user string, launchers, operators []string) doctorCheck {
	if !held {
		return doctorCheck{ID: "runs-bucket-iam", OK: true}
	}
	m := "user:" + user
	const text = "your credentials can write anywhere in gs://%s: this installation predates the 0.7.0 bucket hardening"
	switch {
	case user != "" && slices.Contains(operators, m):
		return doctorCheck{ID: "runs-bucket-iam", OK: true}
	case user != "" && slices.Contains(launchers, m):
		return doctorCheck{ID: "runs-bucket-iam", Severity: "warning", Problem: fmt.Sprintf(text, bucket),
			Fix: "ask an operator to run fugaro init (0.7.0 or later)"}
	default:
		return doctorCheck{ID: "runs-bucket-iam", Severity: "info", Problem: "expected for an operator; otherwise " + fmt.Sprintf(text, bucket),
			Fix: "if you only launch runs, ask an operator to run fugaro init (0.7.0 or later)"}
	}
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
			Problem: pluginwire.Printable(f.Problem), Fix: f.Fix})
	}
	return append([]doctorCheck{sum}, out...)
}
