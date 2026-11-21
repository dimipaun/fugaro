# Sourced by scripts/release.sh and scripts/release-rehearsal.sh: the one rule
# for the installation a release may never build its own catalog entry in. Both
# read FUGARO_LIVE_GCP_PROJECT and both have to refuse the owner's own the same
# way, so the list lives here rather than in two copies that can drift apart.
#
# The owner's own installation is not spelled one way, which is why this is a
# list and not one word: it was created under the name `belong`, its resources
# run in the GCP project `edge-devel-dimi` and its database is the Firebase
# project `fugaro-belong` (internal/orchestrator/never.go's neverBelong
# comment; docs/m9-budget-and-dashboard/spec.md, D19), and the register's own
# checks refuse `edgeappinc` beside it (scripts/live-checks/check-lc4.sh,
# check291.sh, check-c39.sh).
#
# The case below is check-lc4.sh's guard_belong spelling for spelling -- the
# word anywhere, and `edge-devel-dimi` or `edgeappinc` anywhere -- so a project
# that merely contains one of the three is refused too. That is on purpose and
# it is the direction that fails closed: a name built around the owner's own is
# not a sandbox a release should guess at.
#
# The CLI is NOT this rule, and the release must not lean on it: the CLI's own
# NeverBelong is called by `fugaro orchestrator serve` alone
# (internal/cli/orchestrator_serve.go), so `fugaro image build go --gcp-project
# edge-devel-dimi` is admitted and really builds there. The refusal has to be
# the caller's, which is what this file is for.

# names_the_owners_installation NAME succeeds when NAME is the owner's own
# installation, or is built around one of the names it answers to.
names_the_owners_installation() {
  case "$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')" in
  *belong* | *edge-devel-dimi* | *edgeappinc*) return 0 ;;
  esac
  return 1
}
