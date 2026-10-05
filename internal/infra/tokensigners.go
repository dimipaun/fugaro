package infra

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	iam "google.golang.org/api/iam/v1"
)

// Firebase accepts a custom sign-in token signed by ANY service account of
// the project (live Check 26, step 7: a token signed by fugaro-scheduler was
// accepted). The real boundary of the budget database's identity is therefore
// who can sign as a service account of the Firebase project, not who holds
// the signer account's minter role. TokenSigners lists those principals,
// read-only, from the project's IAM policy and each service account's.

// SignerKind says what a principal can do about signing.
type SignerKind int

const (
	// SignerSigns can sign as an account: the role holds signJwt, signBlob,
	// getAccessToken, implicitDelegation or serviceAccountKeys.create.
	SignerSigns SignerKind = iota + 1
	// SignerGrants can grant itself the right to sign (the role holds
	// iam.serviceAccounts.setIamPolicy), on a single account or all of them.
	SignerGrants
	// SignerUnread: the role's permissions, or the account's policy, could
	// not be read, so what the principal can do is not known.
	SignerUnread
)

// SignerCondition is a conditional binding's condition.
type SignerCondition struct{ Title, Expression string }

// SignerFinding is one principal's binding that signs, can grant signing, or
// could not be read. Account is the service account the binding is on, ""
// when it is on the whole project.
type SignerFinding struct {
	Kind      SignerKind
	Member    string
	Role      string
	Account   string
	Condition *SignerCondition
	// Expected is set for what the design expects (Why says what); everything
	// else is a risk to name.
	Expected bool
	Why      string
	// Perms are the permissions of the role, as the IAM API resolves them,
	// that make it sign or grant (empty for SignerUnread).
	Perms []string
	// Err is why a SignerUnread finding could not be read.
	Err string
}

// Permissions that sign as an account directly (a Firebase custom token is a
// JWT signed by the account's key), and the one that can grant them.
var (
	signPermissions  = []string{"iam.serviceAccounts.signJwt", "iam.serviceAccounts.signBlob", "iam.serviceAccounts.getAccessToken", "iam.serviceAccounts.implicitDelegation", "iam.serviceAccountKeys.create"}
	grantPermissions = []string{"iam.serviceAccounts.setIamPolicy", "resourcemanager.projects.setIamPolicy"}
)

var adminSDKRE = func(fp string) *regexp.Regexp {
	return regexp.MustCompile(`^serviceAccount:firebase-adminsdk-[a-z0-9]{4,6}@` + regexp.QuoteMeta(fp) + `\.iam\.gserviceaccount\.com$`)
}

type signerBinding struct {
	role    string
	members []string
	cond    *SignerCondition
}

// TokenSigners reads the Firebase project fp's IAM policy and the policy of
// each of its service accounts, and returns every principal that holds a
// role that can sign as an account or grant itself that. signer is the token
// signer account's email. A project policy or account list that cannot be
// read is an error; a single account's policy or a single role that cannot be
// read is a SignerUnread finding.
func TokenSigners(ctx context.Context, c *crm.Service, i *iam.Service, fp, signer string) ([]SignerFinding, error) {
	pol, err := c.Projects.GetIamPolicy(fp, &crm.GetIamPolicyRequest{Options: &crm.GetPolicyOptions{RequestedPolicyVersion: 3}}).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("reading the IAM policy of project %s: %w", fp, err)
	}
	var accounts []string
	err = i.Projects.ServiceAccounts.List("projects/"+fp).PageSize(100).Pages(ctx, func(r *iam.ListServiceAccountsResponse) error {
		for _, a := range r.Accounts {
			accounts = append(accounts, a.Email)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("listing the service accounts of project %s: %w", fp, err)
	}
	slices.Sort(accounts)

	e := &signerEval{ctx: ctx, i: i, fp: fp, signer: strings.ToLower(signer), adminSDK: adminSDKRE(fp), roles: map[string]*roleInfo{}}
	var proj []signerBinding
	for _, b := range pol.Bindings {
		sb := signerBinding{role: b.Role, members: b.Members}
		if b.Condition != nil {
			sb.cond = &SignerCondition{Title: b.Condition.Title, Expression: b.Condition.Expression}
		}
		proj = append(proj, sb)
	}
	out := e.eval("", proj)
	for _, a := range accounts {
		p, err := i.Projects.ServiceAccounts.GetIamPolicy("projects/" + fp + "/serviceAccounts/" + a).OptionsRequestedPolicyVersion(3).Context(ctx).Do()
		if err != nil {
			out = append(out, SignerFinding{Kind: SignerUnread, Account: a, Err: firstLineOf(err.Error())})
			continue
		}
		var bs []signerBinding
		for _, b := range p.Bindings {
			sb := signerBinding{role: b.Role, members: b.Members}
			if b.Condition != nil {
				sb.cond = &SignerCondition{Title: b.Condition.Title, Expression: b.Condition.Expression}
			}
			bs = append(bs, sb)
		}
		out = append(out, e.eval(a, bs)...)
	}
	slices.SortFunc(out, func(a, b SignerFinding) int {
		if a.Expected != b.Expected {
			if !a.Expected {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Account+"\x00"+a.Role+"\x00"+a.Member, b.Account+"\x00"+b.Role+"\x00"+b.Member)
	})
	return out, nil
}

func firstLineOf(s string) string {
	l, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return l
}

type roleInfo struct {
	kind    SignerKind // 0: neither signs nor grants
	perms   []string
	matched []string
	err     error
}

type signerEval struct {
	ctx      context.Context
	i        *iam.Service
	fp       string
	signer   string
	adminSDK *regexp.Regexp
	roles    map[string]*roleInfo
}

// role is what a role can do, from its permissions as the IAM API resolves
// them: every role is read, predefined and primitive ones included, so
// nothing rests on a list of names. A role that cannot be read is an error.
func (e *signerEval) role(name string) *roleInfo {
	if r, ok := e.roles[name]; ok {
		return r
	}
	r := &roleInfo{}
	e.roles[name] = r
	var role *iam.Role
	var err error
	switch {
	case strings.HasPrefix(name, "projects/") && strings.Contains(name, "/roles/"):
		role, err = e.i.Projects.Roles.Get(name).Context(e.ctx).Do()
	case strings.HasPrefix(name, "organizations/") && strings.Contains(name, "/roles/"):
		role, err = e.i.Organizations.Roles.Get(name).Context(e.ctx).Do()
	case strings.HasPrefix(name, "roles/"):
		role, err = e.i.Roles.Get(name).Context(e.ctx).Do()
	default:
		err = fmt.Errorf("%q is not a role name", name)
	}
	if err != nil {
		r.err = err
		return r
	}
	r.perms = role.IncludedPermissions
	for _, p := range r.perms {
		if slices.Contains(signPermissions, p) {
			r.kind = SignerSigns
			r.matched = append(r.matched, p)
		}
	}
	if r.kind == 0 {
		for _, p := range r.perms {
			if slices.Contains(grantPermissions, p) {
				r.kind = SignerGrants
				r.matched = append(r.matched, p)
			}
		}
	}
	return r
}

// minterMember: the designed path grants the minter role to people and
// service accounts (and groups), never to allUsers, allAuthenticatedUsers or
// a whole domain.
func minterMember(m string) bool {
	return strings.HasPrefix(m, "user:") || strings.HasPrefix(m, "serviceAccount:") || strings.HasPrefix(m, "group:")
}

// eval is the findings of one policy's bindings; account is "" for the
// project's own.
func (e *signerEval) eval(account string, bs []signerBinding) []SignerFinding {
	var out []SignerFinding
	for _, b := range bs {
		r := e.role(b.role)
		for _, m := range b.members {
			if strings.HasPrefix(m, "deleted:") {
				continue // a principal that no longer exists
			}
			f := SignerFinding{Kind: r.kind, Member: m, Role: b.role, Account: account, Condition: b.cond, Perms: r.matched}
			switch {
			case r.err != nil:
				f.Kind, f.Err = SignerUnread, firstLineOf(r.err.Error())
			case r.kind == 0:
				continue
			}
			switch {
			case f.Kind == SignerSigns && e.adminSDK.MatchString(m):
				f.Expected, f.Why = true, "the Firebase Admin SDK account"
			case f.Kind == SignerSigns && minterMember(m) && account != "" && strings.EqualFold(account, e.signer) &&
				b.role == "projects/"+e.fp+"/roles/"+MinterRoleID && slices.Equal(r.perms, []string{"iam.serviceAccounts.signJwt"}):
				f.Expected, f.Why = true, "the designed path: launchers and operators mint run tokens as the signer"
			}
			out = append(out, f)
		}
	}
	return out
}
