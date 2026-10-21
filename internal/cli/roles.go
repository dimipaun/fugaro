package cli

import (
	"slices"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

// Who an installation's own lists say an identity is -- the CLI's ONE notion
// of "an operator of this installation" and of "a launcher of it", read off
// the same two lists everywhere.
//
// The lists are `terraform.launchers` and `terraform.operators` in the local
// config (localcfg.Terraform), the IAM members `fugaro setup --launcher` and
// `--operator` write, hold and copy back from the installation's own outputs
// (init_adopt.go). The 0.7.0 bucket hardening grants them their roles on the
// runs bucket (docs/design/bucket-iam.md H9), and `fugaro doctor`'s
// runs-bucket-iam check is where the CLI already reads them to place the
// caller (doctor_bucket.go's selfCheck).
//
// A predicate that answers "is this identity an operator" is deliberately not
// shared with anything that *authorises*: nothing here grants a privilege. It
// decides what a table shows by default, and the member text is the local
// config's own, while the identity a run recorded is untrusted and is never
// compared against these lists.

// operatorMember reports whether member ("user:<address>") is listed among
// the installation's operators: the people who onboard repositories and
// submit builds.
func operatorMember(member string, operators []string) bool {
	return member != "" && slices.Contains(operators, member)
}

// launcherMember reports whether member is listed among the installation's
// launchers: the people who may start runs.
func launcherMember(member string, launchers []string) bool {
	return member != "" && slices.Contains(launchers, member)
}

// meMember is who the local config says the caller is, as the IAM member the
// installation's lists are written in: "user:" + the same identity
// localcfg.Me answers with (`user:` in the config, else git config
// user.email), which is the identity `fugaro run` records as a run's
// requester and `fugaro runs ls --mine` filters by.
//
// An identity that cannot be told is the empty member, which is on no list:
// a caller the CLI cannot name is not an operator of the installation.
func meMember(me string) string {
	if me == "" {
		return ""
	}
	return "user:" + me
}

// operatorOf reports whether the local config's lists make me an operator of
// its installation. It is false for an identity that cannot be told -- and for
// an installation whose config carries no operators list, which is every
// installation from before the 0.7.0 hardening: a caller the lists do not
// place is not an operator.
//
// There is deliberately no launcherOf beside it. The two member predicates are
// the shared notion, and what a caller may be shown turns on being an operator
// (runs stats), so a launcherOf would be a function with no caller: the day
// something needs it, it is two lines here rather than a permission nobody
// reads. launcherMember is not dead -- doctor's bucket check places the caller
// as a launcher to say what its unconditioned write access means.
func operatorOf(lc *localcfg.Config, me string) bool {
	return operatorMember(meMember(me), lc.Terraform.Operators)
}
