package cli

import (
	"context"
	"fmt"
	"slices"
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
// identity. Three kinds of principal are information: the ones the design
// expects (the Firebase Admin SDK account, the minter role on the signer
// account alone), Google's own service agents of this project (matched by
// exact email AND this project's number) and the project owners (the trust
// root); none is ever given a remove command. Every other one is a warning
// that fails doctor under --strict. Nothing is
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
	pfp, psigner := pluginwire.Printable(fp), pluginwire.Printable(signer)
	// The project's number tells Google's own service agents of this project
	// from look-alikes; unread, none is recognised (they warn like any other).
	number, nerr := infra.ProjectNumber(ctx, &infra.Clients{CRM: c}, fp)
	if nerr != nil {
		number = 0
	}
	fs, err := infra.TokenSigners(ctx, c, i, fp, signer, number)
	if err != nil {
		// A warning: with nothing read, --strict must not go green.
		return []doctorCheck{{ID: "token-signers", Severity: "warning",
			Problem: "cannot tell who can sign sign-in tokens for the budget database in project " + pfp + ": " + oneLineCLI(err.Error()),
			Fix:     "ask an owner of " + pfp + " to grant you read access to its IAM (for example roles/iam.securityReviewer), or run doctor as one"}}
	}
	var out []doctorCheck
	var expected []string
	agents := map[string][]string{} // Google service agent -> its roles
	var agentOrder []string
	owners := map[string]bool{}
	var ownerOrder []string
	n := 0
	for _, f := range fs {
		switch {
		case f.Expected:
			expected = append(expected, pluginwire.Printable(f.Member))
		case f.Class == infra.SignerGoogleAgent:
			m := pluginwire.Printable(strings.TrimPrefix(f.Member, "serviceAccount:"))
			if _, ok := agents[m]; !ok {
				agentOrder = append(agentOrder, m)
			}
			if r := pluginwire.Printable(f.Role); !slices.Contains(agents[m], r) {
				agents[m] = append(agents[m], r)
			}
		case f.Class == infra.SignerOwner:
			if m := pluginwire.Printable(f.Member); !owners[m] {
				owners[m] = true
				ownerOrder = append(ownerOrder, m)
			}
		default:
			n++
			out = append(out, signerCheck(fmt.Sprintf("token-signers-%d", n), fp, f))
		}
	}
	var info []doctorCheck
	if len(agentOrder) > 0 {
		var parts []string
		for _, m := range agentOrder {
			parts = append(parts, m+" ("+strings.Join(agents[m], ", ")+")")
		}
		info = append(info, doctorCheck{ID: "token-signers-google-agents", Severity: "info",
			Problem: "Google-operated service agents of this project hold roles that can sign tokens or change IAM: " + strings.Join(parts, ", ") +
				"; they are Google-operated, not removable without breaking the service; the trust is the same as trusting Google; no action"})
	}
	if len(ownerOrder) > 0 {
		info = append(info, doctorCheck{ID: "token-signers-owners", Severity: "info",
			Problem: fmt.Sprintf("project owners can create service-account keys and therefore sign tokens: the trust root; %d owner(s): %s; keep this list short",
				len(ownerOrder), strings.Join(ownerOrder, ", "))})
	}
	// Always said, and always partial: nothing found is not "safe".
	msg := "checked the project and service-account IAM policies of " + pfp + "; not read: folder- and organization-inherited bindings, deny policies (docs/gcp-setup.md#token-signers)"
	if nerr == nil {
		msg += "; Google service agents of this project are read and classified"
	} else {
		msg += "; the project number could not be read (" + oneLineCLI(nerr.Error()) + "), so no Google service agent is recognised and each is listed as a warning"
	}
	if len(expected) > 0 {
		msg += fmt.Sprintf("; %d principal(s) can sign as designed (the Firebase Admin SDK account; launchers and operators through the minter role on the signer account): %s",
			len(expected), strings.Join(dedupe(expected), ", "))
	}
	summary := doctorCheck{ID: "token-signers", Severity: "info", Problem: msg,
		Fix: "to review the signer's policy: gcloud iam service-accounts get-iam-policy " + shellword.Quote(psigner) + " --project " + shellword.Quote(pfp)}
	return append(append([]doctorCheck{summary}, info...), out...)
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
	pfp := pluginwire.Printable(fp)
	where := "project " + pfp
	if f.Account != "" {
		where = "service account " + account
	}
	cond := ""
	if f.Condition != nil {
		cond = " (only while " + pluginwire.Printable(f.Condition.Title) + ")"
	}
	perms := pluginwire.Printable(strings.Join(f.Perms, ", "))
	c := doctorCheck{ID: id, Severity: "warning"}
	switch f.Kind {
	case infra.SignerSigns:
		c.Problem = fmt.Sprintf("%s holds %s on %s%s (which the IAM API resolves to include %s) and %s", member, role, where, cond, perms, signConsequence)
	case infra.SignerGrants:
		c.Problem = fmt.Sprintf("%s holds %s on %s%s (which the IAM API resolves to include %s) and can grant itself the right to sign sign-in tokens with any claims, i.e. act as any run against the budget database", member, role, where, cond, perms)
	default:
		if f.Member == "" {
			c.Problem = fmt.Sprintf("cannot tell who holds the right to sign on %s: its IAM policy was not read (%s)", where, pluginwire.Printable(f.Err))
			c.Fix = "ask an owner to grant you iam.serviceAccounts.getIamPolicy on " + account + ", or review it yourself: gcloud iam service-accounts get-iam-policy " + shellword.Quote(account) + " --project " + shellword.Quote(pfp)
			return c
		}
		c.Problem = fmt.Sprintf("cannot tell what %s can do with %s on %s: the role was not read (%s); it may be able to sign sign-in tokens", member, role, where, pluginwire.Printable(f.Err))
		c.Fix = "ask an owner to grant you iam.roles.get on the role, or read it yourself: gcloud iam roles describe " + shellword.Quote(role)
		return c
	}
	cmd := "gcloud projects remove-iam-policy-binding " + shellword.Quote(pfp)
	if f.Account != "" {
		cmd = "gcloud iam service-accounts remove-iam-policy-binding " + shellword.Quote(account) + " --project " + shellword.Quote(pfp)
	}
	// Printable forms: a member with a control character in it is shown
	// escaped, never as bytes a terminal would act on.
	cmd += " --member=" + shellword.Quote(member) + " --role=" + shellword.Quote(role)
	if f.Condition != nil {
		cmd += " --condition=" + shellword.Quote("expression="+pluginwire.Printable(f.Condition.Expression)+",title="+pluginwire.Printable(f.Condition.Title))
	}
	c.Fix = "review before running: " + cmd
	return c
}
