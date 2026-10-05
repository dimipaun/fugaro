package cli

import (
	"context"
	"fmt"
	"strings"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	iam "google.golang.org/api/iam/v1"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/shellword"
)

// signConsequence is what holding the right to sign means for the budget
// database (design m9-budget-and-dashboard.md, "Run tokens are accepted per project").
const signConsequence = "can sign sign-in tokens with any claims, i.e. act as any run against the budget database"

// doctorTokenSigners is the token-signers check: who can sign as a service
// account of the Firebase project. Firebase accepts a custom sign-in token
// signed by ANY service account of the project, so that, and not the signer
// account's minter role, is the real boundary of the budget database's
// identity. Only the principals the design expects (the Firebase Admin SDK
// account, and the minter role on the signer account alone) are information;
// every other one is a warning that fails doctor under --strict. Nothing is
// checked without a budget backend. Read-only.
func doctorTokenSigners(ctx context.Context, lc *localcfg.Config, c *crm.Service, i *iam.Service) []doctorCheck {
	if lc.Budget == nil || lc.Budget.FirebaseProject == "" {
		return nil
	}
	fp := lc.Budget.FirebaseProject
	signer := lc.Budget.TokenSigner
	if signer == "" {
		signer = infra.SignerAccountID + "@" + fp + ".iam.gserviceaccount.com"
	}
	fs, err := infra.TokenSigners(ctx, c, i, fp, signer)
	if err != nil {
		return []doctorCheck{{ID: "token-signers", Severity: "info",
			Problem: "cannot tell who can sign sign-in tokens for the budget database in project " + pluginwire.Printable(fp) + ": " + oneLineCLI(err.Error()),
			Fix:     "ask an owner of " + pluginwire.Printable(fp) + " to grant you read access to its IAM (for example roles/iam.securityReviewer), or run doctor as one"}}
	}
	var out []doctorCheck
	var expected []string
	n := 0
	for _, f := range fs {
		if f.Expected {
			expected = append(expected, pluginwire.Printable(f.Member))
			continue
		}
		n++
		out = append(out, signerCheck(fmt.Sprintf("token-signers-%d", n), fp, f))
	}
	if n > 0 {
		return out
	}
	if len(expected) == 0 {
		return []doctorCheck{{ID: "token-signers", OK: true}}
	}
	return []doctorCheck{{ID: "token-signers", Severity: "info",
		Problem: fmt.Sprintf("%d principal(s) can sign sign-in tokens for the budget database, all as designed (the Firebase Admin SDK account; launchers and operators through the minter role on the signer account): %s",
			len(expected), strings.Join(dedupe(expected), ", ")),
		Fix: "no action needed; to review: gcloud iam service-accounts get-iam-policy " + shellword.Quote(signer) + " --project " + shellword.Quote(fp)}}
}

func dedupe(s []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range s {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// signerCheck is the warning for one unexpected finding, with the one-line
// command that removes the binding. Text from the policy (members, roles) is
// made printable, so a hostile name cannot dress the line up.
func signerCheck(id, fp string, f infra.SignerFinding) doctorCheck {
	member, role, account := pluginwire.Printable(f.Member), pluginwire.Printable(f.Role), pluginwire.Printable(f.Account)
	where := "project " + pluginwire.Printable(fp)
	if f.Account != "" {
		where = "service account " + account
	}
	cond := ""
	if f.Condition != nil {
		cond = " (only while " + pluginwire.Printable(f.Condition.Title) + ")"
	}
	c := doctorCheck{ID: id, Severity: "warning"}
	switch f.Kind {
	case infra.SignerSigns:
		c.Problem = fmt.Sprintf("%s holds %s on %s%s and %s", member, role, where, cond, signConsequence)
	case infra.SignerGrants:
		c.Problem = fmt.Sprintf("%s holds %s on %s%s and can grant itself the right to sign sign-in tokens with any claims, i.e. act as any run against the budget database", member, role, where, cond)
	default:
		if f.Member == "" {
			c.Problem = fmt.Sprintf("cannot tell who holds the right to sign on %s: its IAM policy was not read (%s)", where, pluginwire.Printable(f.Err))
			c.Fix = "ask an owner to grant you iam.serviceAccounts.getIamPolicy on " + account + ", or review it yourself: gcloud iam service-accounts get-iam-policy " + shellword.Quote(account) + " --project " + shellword.Quote(fp)
			return c
		}
		c.Problem = fmt.Sprintf("cannot tell what %s can do with %s on %s: the role was not read (%s); it may be able to sign sign-in tokens", member, role, where, pluginwire.Printable(f.Err))
		c.Fix = "ask an owner to grant you iam.roles.get on the role, or read it yourself: gcloud iam roles describe " + shellword.Quote(f.Role)
		return c
	}
	cmd := "gcloud projects remove-iam-policy-binding " + shellword.Quote(fp)
	if f.Account != "" {
		cmd = "gcloud iam service-accounts remove-iam-policy-binding " + shellword.Quote(account) + " --project " + shellword.Quote(fp)
	}
	// Printable forms: a member with a control character in it is shown
	// escaped, never as bytes a terminal would act on.
	cmd += " --member=" + shellword.Quote(member) + " --role=" + shellword.Quote(role)
	if f.Condition != nil {
		cmd += " --condition=" + shellword.Quote("expression="+pluginwire.Printable(f.Condition.Expression)+",title="+pluginwire.Printable(f.Condition.Title))
	}
	c.Fix = cmd
	return c
}
